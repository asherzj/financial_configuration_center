package rpcauth_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
	kitexcommonv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/common/v1"
	kitexconfigv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1/configservice"
	"github.com/cloudwego/kitex/client"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/server"
)

func TestKitexTransportRunsConsumerAuthenticationForUnaryAndWatch(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authenticator := newAuthenticator(t,
		consumerVerifierFunc(func(_ context.Context, token string) (platformauth.ConsumerIdentity, error) {
			if token != "consumer-token" {
				return platformauth.ConsumerIdentity{}, platformauth.ErrTokenInvalid
			}
			return platformauth.ConsumerIdentity{ConsumerID: "payments", JWTID: "transport-jti"}, nil
		}),
		internalVerifierFunc(func(context.Context, string) (platformauth.InternalCallerIdentity, error) {
			return platformauth.InternalCallerIdentity{}, platformauth.ErrTokenInvalid
		}),
	)
	authOptions, err := rpcauth.KitexServerOptions(authenticator)
	if err != nil {
		t.Fatal(err)
	}
	options := []server.Option{server.WithListener(listener), server.WithExitWaitTime(time.Second)}
	options = append(options, authOptions...)
	service := &authenticatedConfigService{}
	kitexServer := configservice.NewServer(service, options...)
	stopped := make(chan error, 1)
	go func() { stopped <- kitexServer.Run() }()
	t.Cleanup(func() {
		if err := kitexServer.Stop(); err != nil {
			t.Errorf("stop Kitex server: %v", err)
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("run Kitex server: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Kitex server did not stop")
		}
	})

	kitexClient, err := configservice.NewClient("ConfigService", client.WithHostPorts(listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	authorized := kitexmetadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer consumer-token")
	authorized, cancel := context.WithTimeout(authorized, 5*time.Second)
	defer cancel()
	for {
		response, err := kitexClient.GetSnapshot(authorized, &kitexconfigv1.GetSnapshotRequest{})
		if err == nil {
			if response.GetSnapshot().GetServerEpoch() != "authenticated" {
				t.Fatalf("unexpected unary response: %+v", response)
			}
			break
		}
		if kitexstatus.Code(err) != kitexcodes.Unavailable {
			t.Fatalf("authenticated unary call: %v", err)
		}
		select {
		case <-authorized.Done():
			t.Fatal(authorized.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	if _, err := kitexClient.GetSnapshot(context.Background(), &kitexconfigv1.GetSnapshotRequest{}); kitexstatus.Code(err) != kitexcodes.Unauthenticated {
		t.Fatalf("missing unary credentials code=%v err=%v", kitexstatus.Code(err), err)
	}
	unauthenticatedStream, err := kitexClient.Watch(context.Background(), &kitexconfigv1.WatchRequest{})
	if err == nil {
		_, err = unauthenticatedStream.Recv()
	}
	if kitexstatus.Code(err) != kitexcodes.Unauthenticated || service.watchCalls.Load() != 0 {
		t.Fatalf("missing Watch credentials code=%v handler calls=%d err=%v", kitexstatus.Code(err), service.watchCalls.Load(), err)
	}
	stream, err := kitexClient.Watch(authorized, &kitexconfigv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil || event.GetEventId() != "authenticated-watch" {
		t.Fatalf("watch event=%+v err=%v", event, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("watch completion=%v, want EOF", err)
	}
}

type authenticatedConfigService struct{ watchCalls atomic.Int32 }

func (*authenticatedConfigService) GetSnapshot(ctx context.Context, _ *kitexconfigv1.GetSnapshotRequest) (*kitexconfigv1.GetSnapshotResponse, error) {
	identity, ok := rpcauth.ConsumerIdentityFromContext(ctx)
	if !ok || identity.ConsumerID != "payments" {
		return nil, kitexstatus.Err(kitexcodes.PermissionDenied, "consumer identity not propagated")
	}
	return &kitexconfigv1.GetSnapshotResponse{Snapshot: &kitexcommonv1.SnapshotIdentity{ServerEpoch: "authenticated"}}, nil
}

func (*authenticatedConfigService) DiffVersions(context.Context, *kitexconfigv1.DiffVersionsRequest) (*kitexconfigv1.DiffVersionsResponse, error) {
	return &kitexconfigv1.DiffVersionsResponse{}, nil
}

func (*authenticatedConfigService) GetCollections(context.Context, *kitexconfigv1.GetCollectionsRequest) (*kitexconfigv1.GetCollectionsResponse, error) {
	return &kitexconfigv1.GetCollectionsResponse{}, nil
}

func (service *authenticatedConfigService) Watch(_ *kitexconfigv1.WatchRequest, stream kitexconfigv1.ConfigService_WatchServer) error {
	service.watchCalls.Add(1)
	identity, ok := rpcauth.ConsumerIdentityFromContext(stream.Context())
	if !ok || identity.ConsumerID != "payments" {
		return kitexstatus.Err(kitexcodes.PermissionDenied, "consumer stream identity not propagated")
	}
	return stream.Send(&kitexconfigv1.WatchResponse{EventId: "authenticated-watch"})
}
