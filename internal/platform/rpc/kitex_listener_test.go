package rpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	kitexcommonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	kitexconfigv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1/configservice"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

func TestManagedUnixListenerKitexOptionUsesConcreteListenerWithoutLosingCleanup(t *testing.T) {
	root := shortSocketRoot(t)
	path := filepath.Join(root, "kitex.sock")
	listener, err := listenManagedUnix(path, root, 0o660, os.Getegid())
	if err != nil {
		t.Fatal(err)
	}
	kitexServer := configservice.NewServer(&listenerConfigService{}, listener.KitexServerOption(), server.WithExitWaitTime(time.Second))
	stopped := make(chan error, 1)
	go func() { stopped <- kitexServer.Run() }()

	kitexClient, err := configservice.NewClient("ConfigService",
		client.WithHostPorts("managed-private"),
		client.WithDialer(managedUnixDialer{path: path}),
		client.WithTransportProtocol(transport.GRPC),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		response, callErr := kitexClient.GetSnapshot(ctx, &kitexconfigv1.GetSnapshotRequest{})
		if callErr == nil {
			if response.GetSnapshot().GetServerEpoch() != "managed-listener" {
				t.Fatalf("unexpected response: %+v", response)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(callErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := kitexServer.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Kitex server did not stop")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Kitex Stop removed managed path before Cleanup: %v", err)
	}
	if err := listener.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("managed path remains after Cleanup: %v", err)
	}
}

type managedUnixDialer struct{ path string }

func (dialer managedUnixDialer) DialTimeout(_, _ string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", dialer.path, timeout)
}

type listenerConfigService struct{}

func (*listenerConfigService) GetSnapshot(context.Context, *kitexconfigv1.GetSnapshotRequest) (*kitexconfigv1.GetSnapshotResponse, error) {
	return &kitexconfigv1.GetSnapshotResponse{Snapshot: &kitexcommonv1.SnapshotIdentity{ServerEpoch: "managed-listener"}}, nil
}

func (*listenerConfigService) DiffVersions(context.Context, *kitexconfigv1.DiffVersionsRequest) (*kitexconfigv1.DiffVersionsResponse, error) {
	return &kitexconfigv1.DiffVersionsResponse{}, nil
}

func (*listenerConfigService) GetCollections(context.Context, *kitexconfigv1.GetCollectionsRequest) (*kitexconfigv1.GetCollectionsResponse, error) {
	return &kitexconfigv1.GetCollectionsResponse{}, nil
}

func (*listenerConfigService) Watch(_ *kitexconfigv1.WatchRequest, stream kitexconfigv1.ConfigService_WatchServer) error {
	return stream.Send(&kitexconfigv1.WatchResponse{})
}
