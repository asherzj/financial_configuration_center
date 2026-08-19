package runtime

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitexcommonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	kitexconfigv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/configservice"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/diagnosticsservice"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/pagequeryservice"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/refreshservice"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
	"github.com/cloudwego/kitex/client"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/pkg/serviceinfo"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

func TestPrivateKitexServerRegistersFourServicesOnOneUnixListener(t *testing.T) {
	path := shortRuntimeSocketPath(t)
	services := &testServices{}
	authenticator, err := rpcauth.New(
		consumerVerifier(func(_ context.Context, token string) (platformauth.ConsumerIdentity, error) {
			if token != "consumer-token" {
				return platformauth.ConsumerIdentity{}, platformauth.ErrTokenInvalid
			}
			return platformauth.ConsumerIdentity{ConsumerID: "payments"}, nil
		}),
		internalVerifier(func(_ context.Context, token string) (platformauth.InternalCallerIdentity, error) {
			if token != "internal-token" {
				return platformauth.InternalCallerIdentity{}, platformauth.ErrTokenInvalid
			}
			return platformauth.InternalCallerIdentity{Subject: "control-plane"}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	privateServer, err := newPrivateServer(path, 0o660, os.Getegid(), time.Second, authenticator, HandlerSet{
		Config: services, PageQuery: services, Refresh: services, Diagnostics: services,
	}, testUnixListenerFactory)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- privateServer.Run() }()
	select {
	case <-privateServer.Accepting():
	case <-time.After(3 * time.Second):
		t.Fatal("private Kitex server did not become accepting")
	}
	if err := privateServer.Run(); err == nil {
		t.Fatal("second Run was accepted")
	}
	t.Cleanup(func() {
		if err := privateServer.Stop(); err != nil {
			t.Errorf("stop private Kitex server: %v", err)
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("run private Kitex server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("private Kitex server did not stop")
		}
		if err := privateServer.Cleanup(); err != nil {
			t.Errorf("cleanup private Kitex server: %v", err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("private socket remains after cleanup: %v", err)
		}
	})

	clientOptions := []client.Option{client.WithHostPorts("finconfig-private"), client.WithDialer(unixDialer{path: path}), client.WithTransportProtocol(transport.GRPC)}
	configClient, err := configservice.NewClient("ConfigService", clientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	pageClient, err := pagequeryservice.NewClient("PageQueryService", clientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	refreshClient, err := refreshservice.NewClient("RefreshService", clientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	diagnosticsClient, err := diagnosticsservice.NewClient("DiagnosticsService", clientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	consumerContext := kitexmetadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer consumer-token")
	consumerContext, cancelConsumer := context.WithTimeout(consumerContext, 5*time.Second)
	defer cancelConsumer()
	for {
		response, callErr := configClient.GetSnapshot(consumerContext, &kitexconfigv1.GetSnapshotRequest{})
		if callErr == nil {
			if response.GetSnapshot().GetServerEpoch() != "one-server" {
				t.Fatalf("unexpected ConfigService response: %+v", response)
			}
			break
		}
		if kitexstatus.Code(callErr) != kitexcodes.Unavailable {
			t.Fatalf("ConfigService startup call: %v", callErr)
		}
		select {
		case <-consumerContext.Done():
			t.Fatal(consumerContext.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	stream, err := configClient.Watch(consumerContext, &kitexconfigv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil || event.GetEventId() != "one-server-watch" {
		t.Fatalf("Watch event=%+v err=%v", event, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Watch completion=%v", err)
	}

	internalContext := kitexmetadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer internal-token")
	internalContext, cancelInternal := context.WithTimeout(internalContext, 5*time.Second)
	defer cancelInternal()
	if _, err := pageClient.QueryPage(internalContext, &kitexconfigv1.QueryPageRequest{}); err != nil {
		t.Fatalf("PageQueryService: %v", err)
	}
	if _, err := refreshClient.Notify(internalContext, &kitexconfigv1.NotifyRequest{}); err != nil {
		t.Fatalf("RefreshService: %v", err)
	}
	if _, err := diagnosticsClient.GetSnapshotStatus(internalContext, &kitexconfigv1.GetSnapshotStatusRequest{}); err != nil {
		t.Fatalf("DiagnosticsService.GetSnapshotStatus: %v", err)
	}
	if _, err := diagnosticsClient.GetCollectionStatus(internalContext, &kitexconfigv1.GetCollectionStatusRequest{}); err != nil {
		t.Fatalf("DiagnosticsService.GetCollectionStatus: %v", err)
	}
	if _, err := pageClient.QueryPage(consumerContext, &kitexconfigv1.QueryPageRequest{}); kitexstatus.Code(err) != kitexcodes.Unauthenticated {
		t.Fatalf("Consumer token crossed into Internal service: code=%v err=%v", kitexstatus.Code(err), err)
	}
	if _, err := configClient.GetSnapshot(internalContext, &kitexconfigv1.GetSnapshotRequest{}); kitexstatus.Code(err) != kitexcodes.Unauthenticated {
		t.Fatalf("Internal token crossed into Consumer service: code=%v err=%v", kitexstatus.Code(err), err)
	}
	if services.configCalls.Load() != 1 || services.watchCalls.Load() != 1 || services.pageCalls.Load() != 1 || services.refreshCalls.Load() != 1 || services.diagnosticCalls.Load() != 2 {
		t.Fatalf("unexpected handler calls: config=%d watch=%d page=%d refresh=%d diagnostic=%d", services.configCalls.Load(), services.watchCalls.Load(), services.pageCalls.Load(), services.refreshCalls.Load(), services.diagnosticCalls.Load())
	}
}

func TestPrivateKitexServerRecoversRegistrationPanicAndCleansListener(t *testing.T) {
	path := shortRuntimeSocketPath(t)
	authenticator, handlers := testComposition(t)
	privateServer, err := newPrivateServerWithRegistrar(path, 0o660, os.Getegid(), time.Second, authenticator, handlers, testUnixListenerFactory, func(server.Server, HandlerSet) error {
		panic("duplicate service")
	})
	if err == nil || privateServer != nil {
		t.Fatalf("registration panic result=%v error=%v", privateServer, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after registration panic: %v", err)
	}
}

func TestPrivateKitexServerStopBeforeRunIsFinalAndCleanable(t *testing.T) {
	path := shortRuntimeSocketPath(t)
	authenticator, handlers := testComposition(t)
	privateServer, err := newPrivateServer(path, 0o660, os.Getegid(), time.Second, authenticator, handlers, testUnixListenerFactory)
	if err != nil {
		t.Fatal(err)
	}
	if err := privateServer.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := privateServer.Stop(); err != nil {
		t.Fatalf("repeated Stop: %v", err)
	}
	if err := privateServer.Run(); err == nil {
		t.Fatal("Run after Stop was accepted")
	}
	if err := privateServer.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after Stop-before-Run cleanup: %v", err)
	}
}

func TestPrivateKitexServerImmediateStopAfterRunStartCannotRaceKitexBootstrap(t *testing.T) {
	path := shortRuntimeSocketPath(t)
	authenticator, handlers := testComposition(t)
	privateServer, err := newPrivateServer(path, 0o660, os.Getegid(), time.Second, authenticator, handlers, testUnixListenerFactory)
	if err != nil {
		t.Fatal(err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- privateServer.Run() }()
	waitForRunStarted(t, privateServer)
	if err := privateServer.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("Run after immediate Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("immediate Stop left Kitex running")
	}
	if err := privateServer.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTP2AcceptProbeDoesNotConfuseKernelBacklogWithKitexAcceptLoop(t *testing.T) {
	path := shortRuntimeSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if probeHTTP2Server(path) {
		t.Fatal("a listening socket without an HTTP/2 accept loop was reported accepting")
	}
}

func TestPrivateKitexServerBootstrapStopIsBounded(t *testing.T) {
	path := shortRuntimeSocketPath(t)
	listener, err := testUnixListenerFactory(path, 0o660, os.Getegid())
	if err != nil {
		t.Fatal(err)
	}
	stalled := &stalledKitexServer{release: make(chan struct{})}
	privateServer := &PrivateServer{
		server: stalled, listener: listener, stopWait: 50 * time.Millisecond,
		accepting: make(chan struct{}), runDone: make(chan struct{}),
	}
	runResult := make(chan error, 1)
	go func() { runResult <- privateServer.Run() }()
	waitForRunStarted(t, privateServer)
	started := time.Now()
	stopErr := privateServer.Stop()
	if !errors.Is(stopErr, ErrKitexBootstrapStopTimeout) {
		t.Fatalf("bootstrap Stop error=%v", stopErr)
	}
	if !errors.Is(stopErr, ErrKitexRunExitTimeout) {
		t.Fatalf("stalled Run exit timeout missing: %v", stopErr)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bootstrap Stop exceeded its bound: %v", elapsed)
	}
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("stalled Run result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bootstrap Stop did not release Run")
	}
	if err := privateServer.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateKitexServerRejectsAndCleansNonUnixListener(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tracked := &trackedOwnedListener{Listener: tcpListener}
	authenticator, handlers := testComposition(t)
	privateServer, err := newPrivateServer("ignored", 0o660, os.Getegid(), time.Second, authenticator, handlers, func(string, os.FileMode, int) (ownedListener, error) {
		return tracked, nil
	})
	if err == nil || privateServer != nil {
		t.Fatalf("non-Unix listener result=%v error=%v", privateServer, err)
	}
	if tracked.cleanupCalls.Load() != 1 {
		t.Fatalf("non-Unix listener cleanup calls=%d", tracked.cleanupCalls.Load())
	}
	if connection, err := net.DialTimeout("tcp", tcpListener.Addr().String(), 50*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("rejected non-Unix listener remained open")
	}
}

func TestPrivateKitexServerFailsBeforeBindingWhenCompositionIsIncomplete(t *testing.T) {
	var listenCalls atomic.Int32
	listen := func(string, os.FileMode, int) (ownedListener, error) {
		listenCalls.Add(1)
		return nil, errors.New("must not bind")
	}
	authenticator, err := rpcauth.New(consumerVerifier(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
		return platformauth.ConsumerIdentity{}, nil
	}), internalVerifier(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
		return platformauth.InternalCallerIdentity{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	services := &testServices{}
	if _, err := newPrivateServer("ignored", 0o660, os.Getegid(), time.Second, authenticator, HandlerSet{PageQuery: services, Refresh: services, Diagnostics: services}, listen); err == nil {
		t.Fatal("missing ConfigService handler accepted")
	}
	if listenCalls.Load() != 0 {
		t.Fatal("listener bound before composition validation")
	}
	if _, err := newPrivateServer("ignored", 0o660, os.Getegid(), time.Second, nil, HandlerSet{Config: services, PageQuery: services, Refresh: services, Diagnostics: services}, listen); err == nil {
		t.Fatal("missing authenticator accepted")
	}
	if listenCalls.Load() != 0 {
		t.Fatal("listener bound before authentication validation")
	}
	var typedNil *testServices
	if _, err := newPrivateServer("ignored", 0o660, os.Getegid(), time.Second, authenticator, HandlerSet{Config: typedNil, PageQuery: services, Refresh: services, Diagnostics: services}, listen); err == nil {
		t.Fatal("typed-nil ConfigService handler accepted")
	}
	if listenCalls.Load() != 0 {
		t.Fatal("listener bound for typed-nil handler")
	}
	nilListenerFactory := func(string, os.FileMode, int) (ownedListener, error) { return nil, nil }
	if _, err := newPrivateServer("ignored", 0o660, os.Getegid(), time.Second, authenticator, HandlerSet{Config: services, PageQuery: services, Refresh: services, Diagnostics: services}, nilListenerFactory); err == nil {
		t.Fatal("nil listener without error accepted")
	}
}

type testServices struct {
	configCalls     atomic.Int32
	watchCalls      atomic.Int32
	pageCalls       atomic.Int32
	refreshCalls    atomic.Int32
	diagnosticCalls atomic.Int32
}

type stalledKitexServer struct {
	release  chan struct{}
	stopOnce sync.Once
}

type trackedOwnedListener struct {
	net.Listener
	cleanupCalls atomic.Int32
}

func (listener *trackedOwnedListener) KitexServerOption() server.Option {
	return server.WithListener(listener.Listener)
}

func (listener *trackedOwnedListener) Cleanup() error {
	listener.cleanupCalls.Add(1)
	return nil
}

func (*stalledKitexServer) RegisterService(*serviceinfo.ServiceInfo, interface{}, ...server.RegisterOption) error {
	return nil
}

func (*stalledKitexServer) GetServiceInfos() map[string]*serviceinfo.ServiceInfo { return nil }

func (server *stalledKitexServer) Run() error {
	<-server.release
	return nil
}

func (server *stalledKitexServer) Stop() error {
	server.stopOnce.Do(func() { close(server.release) })
	return nil
}

func waitForRunStarted(t *testing.T, privateServer *PrivateServer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		privateServer.mu.Lock()
		started := privateServer.runStarted
		privateServer.mu.Unlock()
		if started {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not enter the wrapper state machine")
		}
		time.Sleep(time.Millisecond)
	}
}

func testComposition(t *testing.T) (*rpcauth.Authenticator, HandlerSet) {
	t.Helper()
	authenticator, err := rpcauth.New(
		consumerVerifier(func(context.Context, string) (platformauth.ConsumerIdentity, error) {
			return platformauth.ConsumerIdentity{}, nil
		}),
		internalVerifier(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			return platformauth.InternalCallerIdentity{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	services := &testServices{}
	return authenticator, HandlerSet{Config: services, PageQuery: services, Refresh: services, Diagnostics: services}
}

func (service *testServices) GetSnapshot(context.Context, *kitexconfigv1.GetSnapshotRequest) (*kitexconfigv1.GetSnapshotResponse, error) {
	service.configCalls.Add(1)
	return &kitexconfigv1.GetSnapshotResponse{Snapshot: &kitexcommonv1.SnapshotIdentity{ServerEpoch: "one-server"}}, nil
}

func (*testServices) DiffVersions(context.Context, *kitexconfigv1.DiffVersionsRequest) (*kitexconfigv1.DiffVersionsResponse, error) {
	return &kitexconfigv1.DiffVersionsResponse{}, nil
}

func (*testServices) GetCollections(context.Context, *kitexconfigv1.GetCollectionsRequest) (*kitexconfigv1.GetCollectionsResponse, error) {
	return &kitexconfigv1.GetCollectionsResponse{}, nil
}

func (service *testServices) Watch(_ *kitexconfigv1.WatchRequest, stream kitexconfigv1.ConfigService_WatchServer) error {
	service.watchCalls.Add(1)
	return stream.Send(&kitexconfigv1.WatchResponse{EventId: "one-server-watch"})
}

func (service *testServices) QueryPage(context.Context, *kitexconfigv1.QueryPageRequest) (*kitexconfigv1.QueryPageResponse, error) {
	service.pageCalls.Add(1)
	return &kitexconfigv1.QueryPageResponse{}, nil
}

func (service *testServices) Notify(context.Context, *kitexconfigv1.NotifyRequest) (*kitexconfigv1.NotifyResponse, error) {
	service.refreshCalls.Add(1)
	return &kitexconfigv1.NotifyResponse{}, nil
}

func (service *testServices) GetSnapshotStatus(context.Context, *kitexconfigv1.GetSnapshotStatusRequest) (*kitexconfigv1.GetSnapshotStatusResponse, error) {
	service.diagnosticCalls.Add(1)
	return &kitexconfigv1.GetSnapshotStatusResponse{}, nil
}

func (service *testServices) GetCollectionStatus(context.Context, *kitexconfigv1.GetCollectionStatusRequest) (*kitexconfigv1.GetCollectionStatusResponse, error) {
	service.diagnosticCalls.Add(1)
	return &kitexconfigv1.GetCollectionStatusResponse{}, nil
}

type consumerVerifier func(context.Context, string) (platformauth.ConsumerIdentity, error)

func (verifier consumerVerifier) Verify(ctx context.Context, token string) (platformauth.ConsumerIdentity, error) {
	return verifier(ctx, token)
}

type internalVerifier func(context.Context, string) (platformauth.InternalCallerIdentity, error)

func (verifier internalVerifier) Verify(ctx context.Context, token string) (platformauth.InternalCallerIdentity, error) {
	return verifier(ctx, token)
}

type testOwnedUnixListener struct {
	*net.UnixListener
	path string
}

func (listener *testOwnedUnixListener) KitexServerOption() server.Option {
	return server.WithListener(listener.UnixListener)
}

type unixDialer struct{ path string }

func (dialer unixDialer) DialTimeout(_, _ string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", dialer.path, timeout)
}

func testUnixListenerFactory(path string, _ os.FileMode, _ int) (ownedListener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	return &testOwnedUnixListener{UnixListener: listener, path: path}, nil
}

func (listener *testOwnedUnixListener) Cleanup() error { return os.Remove(listener.path) }

func shortRuntimeSocketPath(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "finconfig-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return filepath.Join(root, "backend.sock")
}
