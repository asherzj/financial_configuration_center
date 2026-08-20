package grpc_test

import (
	"context"
	"errors"
	"math"
	"net"
	"testing"
	"time"

	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/diagnosticsservice"
	configgrpc "github.com/asherzj/financial_configuration_center/internal/configserver/grpc"
	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/cloudwego/kitex/client"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

func TestDiagnosticsHandlerProjectsOnlySnapshotMetadata(t *testing.T) {
	t.Parallel()
	provider := diagnosticsProvider{value: snapshot.Diagnostics{
		Identity:               snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 4, PublishedAt: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)},
		Environment:            "production",
		Collections:            []snapshot.CollectionDiagnostic{{Name: "routes", Revision: 8, Cursor: 34, Digest: readmodel.Digest{Algorithm: "SHA-256", Value: "digest"}}},
		FailedDependencyGroups: [][]string{{"routes", "options"}},
		LastErrorCode:          "DEPENDENCY_GROUP_FAILED",
	}}
	handler, err := configgrpc.NewDiagnostics(provider, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	status, err := handler.GetSnapshotStatus(context.Background(), &configv1.GetSnapshotStatusRequest{})
	if err != nil || status.CollectionCount != 1 || status.Snapshot.SnapshotGeneration != 4 || status.Environment != "production" || len(status.Collections) != 1 || status.Collections[0].ConfigRevision != 8 || status.Collections[0].ChangeCursor != 34 || len(status.FailedDependencyGroups) != 1 || status.FailedDependencyGroups[0] != "routes,options" || len(status.FailedDependencyGroupDetails) != 1 || len(status.FailedDependencyGroupDetails[0].Collections) != 2 || status.GetLastErrorCode() != "DEPENDENCY_GROUP_FAILED" {
		t.Fatalf("snapshot status = %+v, %v", status, err)
	}
	collection, err := handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: "production"})
	if err != nil || collection.Version.ConfigRevision != 8 || collection.ChangeCursor != 34 || collection.Version.EffectiveDigest.Value != "digest" {
		t.Fatalf("collection status = %+v, %v", collection, err)
	}
}

func TestDiagnosticsRejectsCursorOutsideRPCRange(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.NewDiagnostics(diagnosticsProvider{value: snapshot.Diagnostics{
		Environment: "production",
		Collections: []snapshot.CollectionDiagnostic{{Name: "routes", Revision: 1, Cursor: math.MaxUint64}},
	}}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: "production"})
	if kitexstatus.Code(err) != kitexcodes.Internal {
		t.Fatalf("cursor overflow code = %s, err %v", kitexstatus.Code(err), err)
	}
}

func TestDiagnosticsRejectsSnapshotOutsideManagedEnvironment(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.NewDiagnostics(diagnosticsProvider{value: snapshot.Diagnostics{Environment: "staging"}}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.GetSnapshotStatus(context.Background(), &configv1.GetSnapshotStatusRequest{}); kitexstatus.Code(err) != kitexcodes.FailedPrecondition {
		t.Fatalf("snapshot status error = %v", err)
	}
	if _, err := handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: "staging"}); kitexstatus.Code(err) != kitexcodes.FailedPrecondition {
		t.Fatalf("collection status error = %v", err)
	}
}

func TestDiagnosticsAuthorizesRoleAndEnvironmentBeforeReadingProvider(t *testing.T) {
	t.Parallel()
	denied := errors.New("diagnostics denied")
	provider := &recordingDiagnosticsProvider{}
	authorizer := &recordingDiagnosticsAuthorizer{err: denied}
	handler, err := configgrpc.NewDiagnostics(provider, authorizer, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.GetSnapshotStatus(context.Background(), &configv1.GetSnapshotStatusRequest{}); !errors.Is(err, denied) {
		t.Fatalf("snapshot diagnostics error=%v", err)
	}
	if provider.called || authorizer.environment != "production" {
		t.Fatalf("provider called=%v environment=%q", provider.called, authorizer.environment)
	}
}

func TestNewDiagnosticsRejectsTypedNilDependencies(t *testing.T) {
	t.Parallel()
	var provider *recordingDiagnosticsProvider
	if _, err := configgrpc.NewDiagnostics(provider, allowRequestAuthorizer{}, "production"); err == nil {
		t.Fatal("expected typed nil provider rejection")
	}
	var authorizer *recordingDiagnosticsAuthorizer
	if _, err := configgrpc.NewDiagnostics(diagnosticsProvider{}, authorizer, "production"); err == nil {
		t.Fatal("expected typed nil authorizer rejection")
	}
}

func TestDiagnosticsPreservesStatusAcrossKitexTransport(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := configgrpc.NewDiagnostics(diagnosticsProvider{value: snapshot.Diagnostics{
		Identity:    snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "snapshot", Generation: 1, PublishedAt: time.Now().UTC()},
		Environment: "production",
	}}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	kitexServer := diagnosticsservice.NewServer(handler, server.WithListener(listener), server.WithExitWaitTime(time.Second))
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
	kitexClient, err := diagnosticsservice.NewClient("DiagnosticsService", client.WithHostPorts(listener.Addr().String()), client.WithTransportProtocol(transport.GRPC))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, err := kitexClient.GetSnapshotStatus(ctx, &configv1.GetSnapshotStatusRequest{}); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	for name, request := range map[string]*configv1.GetCollectionStatusRequest{
		"missing": {Collection: "missing", Environment: "production"},
		"routing": {Collection: "missing", Environment: "staging"},
	} {
		want := kitexcodes.NotFound
		if name == "routing" {
			want = kitexcodes.FailedPrecondition
		}
		if _, err := kitexClient.GetCollectionStatus(ctx, request); kitexstatus.Code(err) != want {
			t.Fatalf("%s code=%v error=%v", name, kitexstatus.Code(err), err)
		}
	}
}

func TestCollectionDiagnosticsNormalizesThenChecksRoutingBeforeAuthorization(t *testing.T) {
	t.Parallel()
	t.Run("normalizes", func(t *testing.T) {
		denied := errors.New("diagnostics denied")
		provider := &recordingDiagnosticsProvider{}
		authorizer := &recordingDiagnosticsAuthorizer{err: denied}
		handler, err := configgrpc.NewDiagnostics(provider, authorizer, "production")
		if err != nil {
			t.Fatal(err)
		}
		_, err = handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: " production "})
		if !errors.Is(err, denied) || provider.called || authorizer.environment != "production" {
			t.Fatalf("error=%v provider=%v environment=%q", err, provider.called, authorizer.environment)
		}
	})
	t.Run("routing", func(t *testing.T) {
		provider := &recordingDiagnosticsProvider{}
		authorizer := &recordingDiagnosticsAuthorizer{}
		handler, err := configgrpc.NewDiagnostics(provider, authorizer, "production")
		if err != nil {
			t.Fatal(err)
		}
		_, err = handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: "staging"})
		if kitexstatus.Code(err) != kitexcodes.FailedPrecondition || provider.called || authorizer.environment != "" {
			t.Fatalf("code=%v provider=%v environment=%q", kitexstatus.Code(err), provider.called, authorizer.environment)
		}
	})
}

type diagnosticsProvider struct{ value snapshot.Diagnostics }

func (provider diagnosticsProvider) Diagnostics() snapshot.Diagnostics { return provider.value }

type recordingDiagnosticsProvider struct{ called bool }

func (provider *recordingDiagnosticsProvider) Diagnostics() snapshot.Diagnostics {
	provider.called = true
	return snapshot.Diagnostics{}
}

type recordingDiagnosticsAuthorizer struct {
	err         error
	environment string
}

func (authorizer *recordingDiagnosticsAuthorizer) AuthorizeDiagnostics(_ context.Context, environment string) error {
	authorizer.environment = environment
	return authorizer.err
}
