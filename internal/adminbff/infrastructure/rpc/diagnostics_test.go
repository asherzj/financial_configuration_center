package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/common/v1"
	configv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/diagnosticsservice"
	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	"github.com/cloudwego/kitex/client/callopt"
	kitexcodes "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	kitexstatus "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDiagnosticsClientMapsStructuredSnapshotAndCollection(t *testing.T) {
	t.Parallel()

	digestValue := strings.Repeat("a", 64)
	lastError := "DEPENDENCY_GROUP_FAILED"
	publishedAt := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	stub := &diagnosticsServiceStub{
		snapshot: &configv1.GetSnapshotStatusResponse{
			Snapshot: &commonv1.SnapshotIdentity{
				ServerEpoch: "epoch", ServerInstanceId: "server", SnapshotInstance: "snapshot", SnapshotGeneration: 3, PublishedAt: timestamppb.New(publishedAt),
			},
			CollectionCount: 1, Environment: "production",
			Collections: []*configv1.SnapshotCollectionStatus{{
				Collection: "routes", ConfigRevision: 8, ChangeCursor: 34,
				EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: digestValue},
			}},
			FailedDependencyGroups:       []string{"routes,options"},
			FailedDependencyGroupDetails: []*configv1.FailedDependencyGroup{{Collections: []string{"routes", "options"}}},
			LastErrorCode:                &lastError,
		},
		collection: &configv1.GetCollectionStatusResponse{
			Collection: "routes", Environment: "production", ChangeCursor: 34, LastErrorCode: &lastError,
			Version: &configv1.VersionView{
				Collection: "routes", ConfigRevision: 8,
				EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: digestValue},
			},
		},
	}
	client, err := NewDiagnosticsClient(stub, " production ")
	if err != nil {
		t.Fatal(err)
	}
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "trace")
	snapshotResult, err := client.ReadSnapshotDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stub.snapshotContext != ctx || snapshotResult.Environment != "production" || snapshotResult.Identity.PublishedAt != publishedAt || snapshotResult.Collections[0].Revision != 8 || snapshotResult.Collections[0].Cursor != 34 || snapshotResult.LastErrorCode != lastError {
		t.Fatalf("snapshot result=%+v context=%v", snapshotResult, stub.snapshotContext)
	}
	if len(snapshotResult.FailedDependencyGroups) != 1 || len(snapshotResult.FailedDependencyGroups[0]) != 2 || snapshotResult.FailedDependencyGroups[0][1] != "options" {
		t.Fatalf("failed groups = %+v", snapshotResult.FailedDependencyGroups)
	}
	collectionResult, err := client.ReadCollectionDiagnostics(ctx, "routes")
	if err != nil {
		t.Fatal(err)
	}
	if stub.collectionContext != ctx || stub.collectionRequest.Collection != "routes" || stub.collectionRequest.Environment != "production" || collectionResult.Revision != 8 || collectionResult.Digest.Value != digestValue || collectionResult.LastErrorCode != lastError {
		t.Fatalf("collection result=%+v request=%+v", collectionResult, stub.collectionRequest)
	}
	stub.snapshot.Collections[0].Collection = "mutated"
	stub.snapshot.FailedDependencyGroupDetails[0].Collections[0] = "mutated"
	if snapshotResult.Collections[0].Name != "routes" || snapshotResult.FailedDependencyGroups[0][0] != "routes" {
		t.Fatalf("mapped diagnostics alias transport response: %+v", snapshotResult)
	}
}

func TestDiagnosticsClientMapsNotFoundWithoutLeakingServerDetail(t *testing.T) {
	t.Parallel()
	client, err := NewDiagnosticsClient(&diagnosticsServiceStub{collectionErr: kitexstatus.Err(kitexcodes.NotFound, "sensitive topology detail")}, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ReadCollectionDiagnostics(context.Background(), "routes")
	if !errors.Is(err, bffapp.ErrDiagnosticsNotFound) || err.Error() != bffapp.ErrDiagnosticsNotFound.Error() {
		t.Fatalf("error = %v", err)
	}
}

func TestDiagnosticsClientRejectsInconsistentSnapshotCompatibilityViews(t *testing.T) {
	t.Parallel()
	digestValue := strings.Repeat("a", 64)
	valid := func() *configv1.GetSnapshotStatusResponse {
		return &configv1.GetSnapshotStatusResponse{
			Snapshot: &commonv1.SnapshotIdentity{
				ServerEpoch: "epoch", ServerInstanceId: "server", SnapshotInstance: "snapshot", SnapshotGeneration: 1, PublishedAt: timestamppb.Now(),
			},
			Environment: "production", CollectionCount: 1,
			Collections: []*configv1.SnapshotCollectionStatus{{
				Collection: "routes", ConfigRevision: 8, EffectiveDigest: &commonv1.Digest{Algorithm: "SHA-256", Value: digestValue},
			}},
			FailedDependencyGroups:       []string{"routes,options"},
			FailedDependencyGroupDetails: []*configv1.FailedDependencyGroup{{Collections: []string{"routes", "options"}}},
		}
	}
	tests := map[string]func() *configv1.GetSnapshotStatusResponse{
		"wrong environment": func() *configv1.GetSnapshotStatusResponse {
			response := valid()
			response.Environment = "staging"
			return response
		},
		"wrong count": func() *configv1.GetSnapshotStatusResponse {
			response := valid()
			response.CollectionCount = 2
			return response
		},
		"wrong group": func() *configv1.GetSnapshotStatusResponse {
			response := valid()
			response.FailedDependencyGroups[0] = "options,routes"
			return response
		},
		"invalid digest": func() *configv1.GetSnapshotStatusResponse {
			response := valid()
			response.Collections[0].EffectiveDigest.Value = "secret"
			return response
		},
	}
	for name, build := range tests {
		name, build := name, build
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := NewDiagnosticsClient(&diagnosticsServiceStub{snapshot: build()}, "production")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ReadSnapshotDiagnostics(context.Background()); !errors.Is(err, ErrInvalidDiagnosticsResponse) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestNewDiagnosticsClientRejectsTypedNilAndInvalidEnvironment(t *testing.T) {
	t.Parallel()
	var client *diagnosticsServiceStub
	if _, err := NewDiagnosticsClient(client, "production"); err == nil {
		t.Fatal("expected typed nil rejection")
	}
	if _, err := NewDiagnosticsClient(&diagnosticsServiceStub{}, "*"); err == nil {
		t.Fatal("expected wildcard environment rejection")
	}
}

type diagnosticsServiceStub struct {
	snapshot          *configv1.GetSnapshotStatusResponse
	snapshotErr       error
	collection        *configv1.GetCollectionStatusResponse
	collectionErr     error
	snapshotContext   context.Context
	collectionContext context.Context
	collectionRequest *configv1.GetCollectionStatusRequest
}

func (stub *diagnosticsServiceStub) GetSnapshotStatus(ctx context.Context, _ *configv1.GetSnapshotStatusRequest, _ ...callopt.Option) (*configv1.GetSnapshotStatusResponse, error) {
	stub.snapshotContext = ctx
	return stub.snapshot, stub.snapshotErr
}

func (stub *diagnosticsServiceStub) GetCollectionStatus(ctx context.Context, request *configv1.GetCollectionStatusRequest, _ ...callopt.Option) (*configv1.GetCollectionStatusResponse, error) {
	stub.collectionContext = ctx
	stub.collectionRequest = request
	return stub.collection, stub.collectionErr
}

var _ diagnosticsservice.Client = (*diagnosticsServiceStub)(nil)
