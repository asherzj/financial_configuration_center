package rpc_test

import (
	"crypto/tls"
	"fmt"
	"os"
	"testing"

	platformrpc "github.com/asherzj/financial_configuration_center/internal/platform/rpc"
)

func TestStandardGRPCClientOptionsRequireVerifiedModernTLS(t *testing.T) {
	t.Parallel()

	if _, err := platformrpc.StandardGRPCClientOptions(nil); err == nil {
		t.Fatal("nil TLS config succeeded")
	}
	if _, err := platformrpc.StandardGRPCClientOptions(&tls.Config{InsecureSkipVerify: true}); err == nil { //nolint:gosec // negative contract fixture
		t.Fatal("InsecureSkipVerify succeeded")
	}
	if _, err := platformrpc.StandardGRPCClientOptions(&tls.Config{MinVersion: tls.VersionTLS11}); err == nil { //nolint:staticcheck // negative contract fixture
		t.Fatal("TLS 1.1 succeeded")
	}
	options, err := platformrpc.StandardGRPCClientOptions(&tls.Config{ServerName: "config-server.finconfig.svc"})
	if err != nil {
		t.Fatalf("valid TLS config: %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("option count = %d, want transport and TLS", len(options))
	}
}

func TestValidatePrivateBackendPathOnlyAcceptsSamePodSocket(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{"127.0.0.1:9000", "/tmp/backend.sock", "/var/run/finconfig/../public.sock", "/var/run/finconfig/backend"} {
		if err := platformrpc.ValidatePrivateBackendPath(invalid); err == nil {
			t.Fatalf("unsafe backend address %q accepted", invalid)
		}
	}
	if err := platformrpc.ValidatePrivateBackendPath("/var/run/finconfig/backend.sock"); err != nil {
		t.Fatalf("private backend path: %v", err)
	}
}

func TestPrivateBackendModeIsRestrictiveOctal(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0620", "0660"} {
		mode, err := platformrpc.ParsePrivateBackendMode(value)
		if err != nil || mode != mustMode(t, value) {
			t.Fatalf("mode %q = %#o, %v", value, mode, err)
		}
	}
	for _, value := range []string{"0600", "0640", "660", "0666", "0770", "0460", "not-mode"} {
		if _, err := platformrpc.ParsePrivateBackendMode(value); err == nil {
			t.Fatalf("unsafe mode %q accepted", value)
		}
	}
}

func mustMode(t *testing.T, value string) os.FileMode {
	t.Helper()
	var mode uint32
	if _, err := fmt.Sscanf(value, "%o", &mode); err != nil {
		t.Fatal(err)
	}
	return os.FileMode(mode)
}
