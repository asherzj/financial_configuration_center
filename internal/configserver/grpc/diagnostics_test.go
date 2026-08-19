package grpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	configgrpc "github.com/asherzj/financial_configuration_center/internal/configserver/grpc"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	configv1 "github.com/asherzj/financial_configuration_center/kitex_gen/finconfig/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDiagnosticsHandlerProjectsOnlySnapshotMetadata(t *testing.T) {
	t.Parallel()
	provider := diagnosticsProvider{value: snapshot.Diagnostics{
		Identity:               snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 4, PublishedAt: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)},
		Environment:            "production",
		Collections:            []snapshot.CollectionDiagnostic{{Name: "routes", Revision: 8, Digest: catalog.Digest{Algorithm: "SHA-256", Value: "digest"}}},
		FailedDependencyGroups: [][]string{{"routes", "options"}},
	}}
	handler, err := configgrpc.NewDiagnostics(provider, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	status, err := handler.GetSnapshotStatus(context.Background(), &configv1.GetSnapshotStatusRequest{})
	if err != nil || status.CollectionCount != 1 || status.Snapshot.SnapshotGeneration != 4 || len(status.FailedDependencyGroups) != 1 || status.FailedDependencyGroups[0] != "routes,options" {
		t.Fatalf("snapshot status = %+v, %v", status, err)
	}
	collection, err := handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: "production"})
	if err != nil || collection.Version.ConfigRevision != 8 || collection.Version.EffectiveDigest.Value != "digest" {
		t.Fatalf("collection status = %+v, %v", collection, err)
	}
}

func TestDiagnosticsRejectsSnapshotOutsideManagedEnvironment(t *testing.T) {
	t.Parallel()
	handler, err := configgrpc.NewDiagnostics(diagnosticsProvider{value: snapshot.Diagnostics{Environment: "staging"}}, allowRequestAuthorizer{}, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.GetSnapshotStatus(context.Background(), &configv1.GetSnapshotStatusRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("snapshot status error = %v", err)
	}
	if _, err := handler.GetCollectionStatus(context.Background(), &configv1.GetCollectionStatusRequest{Collection: "routes", Environment: "staging"}); status.Code(err) != codes.FailedPrecondition {
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
		if status.Code(err) != codes.FailedPrecondition || provider.called || authorizer.environment != "" {
			t.Fatalf("code=%v provider=%v environment=%q", status.Code(err), provider.called, authorizer.environment)
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
