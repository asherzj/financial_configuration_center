package rpc_test

import (
	"context"
	"errors"
	"math"
	"net"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/pagequeryservice"
	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	accessrpc "github.com/asherzj/financial_configuration_center/internal/access/infrastructure/rpc"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/client/callopt"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

func TestSnapshotAuthorityReaderMapsPageQueryMetadata(t *testing.T) {
	t.Parallel()
	client := &pageQueryClientStub{response: validResponse(math.MaxInt64, math.MaxInt64)}
	reader, err := accessrpc.NewSnapshotAuthorityReader(client)
	if err != nil {
		t.Fatal(err)
	}
	bucket := int32(42)
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	authority, err := reader.ReadSnapshotAuthority(ctx, access.SnapshotAuthorityQuery{
		ModelCode: "credentials", Scope: access.Scope{Region: "cn", Environment: "production", Stage: "blue"}, PreviewBucket: &bucket,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.ctx != ctx || client.request == nil || client.request.ModelCode != "credentials" || client.request.Scope == nil || client.request.Scope.Region != "cn" || client.request.Scope.Environment != "production" || client.request.Scope.Stage != "blue" {
		t.Fatalf("context/request = %v %+v", client.ctx, client.request)
	}
	if client.request.QueryType != commonv1.QueryPageType_QUERY_PAGE_TYPE_ONLY_DATA || client.request.Page == nil || client.request.Page.GetNumber() != 1 || client.request.Page.GetSize() != 1 || client.request.PreviewBucket == nil || *client.request.PreviewBucket != 42 {
		t.Fatalf("query options = %+v", client.request)
	}
	if !authority.Found || authority.Environment != "production" || authority.ServerEpoch != "epoch" || authority.SnapshotInstance != "instance" || authority.SnapshotGeneration != 3 || uint64(authority.ModelRevision) != math.MaxInt64 || uint64(authority.CollectionRevision) != math.MaxInt64 {
		t.Fatalf("authority = %+v", authority)
	}
}

func TestSnapshotAuthorityReaderMapsAvailabilityWithoutLeakingRows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response *configv1.QueryPageResponse
		err      error
		found    bool
		want     error
	}{
		{name: "not found", err: kitexstatus.Err(kitexcodes.NotFound, "model missing")},
		{name: "wrong environment", err: kitexstatus.Err(kitexcodes.FailedPrecondition, "wrong environment")},
		{name: "snapshot unavailable", err: kitexstatus.Err(kitexcodes.Unavailable, "not loaded"), want: access.ErrSnapshotUnavailable},
		{name: "malformed response", response: &configv1.QueryPageResponse{}, want: accessrpc.ErrInvalidSnapshotAuthorityResponse},
		{name: "success", response: validResponse(7, 8), found: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, err := accessrpc.NewSnapshotAuthorityReader(&pageQueryClientStub{response: test.response, err: test.err})
			if err != nil {
				t.Fatal(err)
			}
			authority, err := reader.ReadSnapshotAuthority(context.Background(), validQuery())
			if !errors.Is(err, test.want) || authority.Found != test.found {
				t.Fatalf("authority=%+v error=%v, want found=%v error=%v", authority, err, test.found, test.want)
			}
		})
	}
}

func TestSnapshotAuthorityReaderPreservesUnexpectedRPCFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("transport failed")
	reader, err := accessrpc.NewSnapshotAuthorityReader(&pageQueryClientStub{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadSnapshotAuthority(context.Background(), validQuery()); !errors.Is(err, want) {
		t.Fatalf("ReadSnapshotAuthority() error = %v, want wrapped %v", err, want)
	}
}

func TestNewSnapshotAuthorityReaderRejectsNilClients(t *testing.T) {
	t.Parallel()
	if _, err := accessrpc.NewSnapshotAuthorityReader(nil); err == nil {
		t.Fatal("NewSnapshotAuthorityReader(nil) succeeded")
	}
	var typedNil *pageQueryClientStub
	if _, err := accessrpc.NewSnapshotAuthorityReader(typedNil); err == nil {
		t.Fatal("NewSnapshotAuthorityReader(typed nil) succeeded")
	}
}

func TestSnapshotAuthorityReaderPreservesStatusAcrossKitexTransport(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	kitexServer := pagequeryservice.NewServer(&transportPageQueryService{}, server.WithListener(listener), server.WithExitWaitTime(time.Second))
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
	kitexClient, err := pagequeryservice.NewClient("PageQueryService", client.WithHostPorts(listener.Addr().String()), client.WithTransportProtocol(transport.GRPC))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := accessrpc.NewSnapshotAuthorityReader(kitexClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := validQuery()
	ready.ModelCode = "ready"
	for {
		if _, err := reader.ReadSnapshotAuthority(ctx, ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	missing := validQuery()
	missing.ModelCode = "missing"
	if authority, err := reader.ReadSnapshotAuthority(ctx, missing); err != nil || authority.Found {
		t.Fatalf("missing authority=%+v error=%v", authority, err)
	}
	unavailable := validQuery()
	unavailable.ModelCode = "unavailable"
	if _, err := reader.ReadSnapshotAuthority(ctx, unavailable); !errors.Is(err, access.ErrSnapshotUnavailable) {
		t.Fatalf("unavailable error=%v", err)
	}
}

func validQuery() access.SnapshotAuthorityQuery {
	return access.SnapshotAuthorityQuery{ModelCode: "credentials", Scope: access.Scope{Region: "cn", Environment: "production"}}
}

func validResponse(modelRevision, collectionRevision int64) *configv1.QueryPageResponse {
	return &configv1.QueryPageResponse{
		ModelCode:     "credentials",
		Snapshot:      &commonv1.SnapshotIdentity{ServerEpoch: "epoch", SnapshotInstance: "instance", SnapshotGeneration: 3},
		ModelRevision: modelRevision, CollectionRevision: collectionRevision,
	}
}

type contextKey struct{}

type transportPageQueryService struct{}

func (transportPageQueryService) QueryPage(_ context.Context, request *configv1.QueryPageRequest) (*configv1.QueryPageResponse, error) {
	switch request.ModelCode {
	case "missing":
		return nil, kitexstatus.Err(kitexcodes.NotFound, "model missing")
	case "unavailable":
		return nil, kitexstatus.Err(kitexcodes.Unavailable, "snapshot unavailable")
	default:
		response := validResponse(7, 8)
		response.ModelCode = request.ModelCode
		return response, nil
	}
}

type pageQueryClientStub struct {
	ctx      context.Context
	request  *configv1.QueryPageRequest
	response *configv1.QueryPageResponse
	err      error
}

func (client *pageQueryClientStub) QueryPage(ctx context.Context, request *configv1.QueryPageRequest, _ ...callopt.Option) (*configv1.QueryPageResponse, error) {
	client.ctx = ctx
	client.request = request
	return client.response, client.err
}
