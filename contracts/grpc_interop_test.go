package contracts_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configv1 "github.com/asherzj/financial_configuration_center/contracts/gen/go/finconfig/config/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

func TestKitexServerInteroperatesWithGeneratedGRPCGoClient(t *testing.T) {
	root := moduleRoot(t)
	serverBinary := filepath.Join(t.TempDir(), "kitex-interop-server")
	build := exec.Command("go", "build", "-o", serverBinary, "./testserver")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Kitex interop server: %v\n%s", err, output)
	}

	cmd := exec.Command(serverBinary, "-listen", "127.0.0.1:0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case err := <-stopped:
			if err != nil && !strings.Contains(err.Error(), "signal") {
				t.Errorf("Kitex server stopped with %v: %s", err, stderr.String())
			}
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("Kitex server did not stop after interrupt")
		}
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read Kitex server address: %v: %s", err, stderr.String())
	}
	address := strings.TrimSpace(strings.TrimPrefix(line, "LISTEN "))
	if address == line || address == "" {
		t.Fatalf("unexpected Kitex server address line %q", line)
	}
	certificatePath, privateKeyPath, roots := writeTestTLSIdentity(t)
	tlsAddress := startTLSTerminatingProxy(t, address, certificatePath, privateKeyPath)

	conn, err := grpc.NewClient(tlsAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs:    roots,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := configv1.NewConfigServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := waitForSnapshot(t, ctx, client)
	if got, want := response.GetSnapshot().GetServerEpoch(), "00000000-0000-7000-8000-000000000001"; got != want {
		t.Fatalf("server epoch = %q, want %q", got, want)
	}

	stream, err := client.Watch(ctx, &configv1.WatchRequest{ConsumerId: "consumer-a", ClientId: "client-a"})
	if err != nil {
		t.Fatalf("open standard gRPC Watch: %v", err)
	}
	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive standard gRPC Watch event: %v", err)
	}
	if event.GetEventId() != "initial" {
		t.Fatalf("event id = %q, want initial", event.GetEventId())
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Watch receive = %v, want EOF", err)
	}

	_, err = client.DiffVersions(ctx, &configv1.DiffVersionsRequest{})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("DiffVersions status = %v, want InvalidArgument", err)
	}
	if len(st.Details()) != 1 {
		t.Fatalf("status detail count = %d, want 1", len(st.Details()))
	}
	if _, ok := st.Details()[0].(*errdetails.BadRequest); !ok {
		t.Fatalf("status detail type = %T, want *errdetails.BadRequest", st.Details()[0])
	}
}

func startTLSTerminatingProxy(t *testing.T, backendAddress, certificatePath, privateKeyPath string) string {
	t.Helper()
	certificate, err := tls.LoadX509KeyPair(certificatePath, privateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	baseListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := tls.NewListener(baseListener, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
	})
	go func() {
		for {
			clientConnection, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			go proxyConnection(ctx, clientConnection, backendAddress)
		}
	}()
	return listener.Addr().String()
}

func proxyConnection(ctx context.Context, clientConnection net.Conn, backendAddress string) {
	defer clientConnection.Close()
	backendConnection, err := net.DialTimeout("tcp", backendAddress, time.Second)
	if err != nil {
		return
	}
	defer backendConnection.Close()
	done := make(chan struct{}, 2)
	copyDirection := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copyDirection(backendConnection, clientConnection)
	go copyDirection(clientConnection, backendConnection)
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func writeTestTLSIdentity(t *testing.T) (certificatePath, privateKeyPath string, roots *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "FinConfig contract test"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificatePath = filepath.Join(directory, "server.crt")
	privateKeyPath = filepath.Join(directory, "server.key")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	if err := os.WriteFile(certificatePath, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("append test server certificate")
	}
	return certificatePath, privateKeyPath, roots
}

func waitForSnapshot(t *testing.T, ctx context.Context, client configv1.ConfigServiceClient) *configv1.GetSnapshotResponse {
	t.Helper()
	for {
		response, err := client.GetSnapshot(ctx, &configv1.GetSnapshotRequest{ConsumerId: "consumer-a", ClientId: "client-a"})
		if err == nil {
			return response
		}
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("standard gRPC GetSnapshot: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
