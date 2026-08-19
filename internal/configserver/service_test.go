package configserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/configserver"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	overlay "github.com/asherzj/financial_configuration_center/internal/overlay/domain"
)

func TestGetSnapshotReturnsOnlyAuthorizedChangedCollections(t *testing.T) {
	t.Parallel()

	manager, key := serverSnapshot(t)
	service := configserver.New(manager, staticAuthorizer{collections: []string{"payment_routes"}}, "production")
	response, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
	})
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(response.Collections) != 1 || response.Collections[0].Name != "payment_routes" || len(response.Collections[0].Records) != 1 {
		t.Fatalf("snapshot response = %+v", response)
	}
	if response.Collections[0].Records[0].RecordKey != key || response.Collections[0].ChangeCursor != 31 || response.Identity.Generation != 1 {
		t.Fatalf("snapshot authority = %+v", response)
	}

	response.Collections[0].Records[0].Data["priority"] = "mutated"
	again, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
		KnownVersions: []configserver.Version{{Collection: "payment_routes", Revision: 7, Digest: "stale"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Collections[0].Records[0].Data["priority"] != "7" {
		t.Fatal("response mutation reached Config Server snapshot")
	}

	currentDigest, _ := manager.Current().CollectionDigest("payment_routes")
	unchanged, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
		KnownVersions: []configserver.Version{{Collection: "payment_routes", Revision: 8, Digest: currentDigest.Value}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Collections) != 0 || len(unchanged.DeletedCollections) != 0 {
		t.Fatalf("unchanged response sent payload: %+v", unchanged)
	}
}

func TestDiffVersionsReturnsStableMutuallyExclusiveChanges(t *testing.T) {
	t.Parallel()
	manager, _ := serverSnapshot(t)
	service := configserver.New(manager, staticAuthorizer{collections: []string{"payment_routes"}}, "production")
	response, err := service.DiffVersions(context.Background(), configserver.DiffVersionsRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
		KnownVersions: []configserver.Version{
			{Collection: " payment_routes ", Revision: 7, Digest: "stale"},
			{Collection: "legacy", Revision: 1, Digest: "legacy"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Added) != 0 || len(response.Modified) != 1 || response.Modified[0] != "payment_routes" ||
		len(response.Deleted) != 1 || response.Deleted[0] != "legacy" || response.Identity.Generation != 1 {
		t.Fatalf("diff response = %+v", response)
	}
	added, err := service.DiffVersions(context.Background(), configserver.DiffVersionsRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
	})
	if err != nil || len(added.Added) != 1 || added.Added[0] != "payment_routes" || len(added.Modified) != 0 || len(added.Deleted) != 0 {
		t.Fatalf("added diff = %+v, %v", added, err)
	}
}

func TestGetCollectionsFiltersRequestsAndNeverReturnsBelowMinimum(t *testing.T) {
	t.Parallel()
	manager, _ := serverSnapshot(t)
	submitter := &recordingRefreshSubmitter{}
	service, err := configserver.NewWithRefresh(manager, staticAuthorizer{collections: []string{"payment_routes"}}, "production", submitter, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.GetCollections(context.Background(), configserver.GetCollectionsRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
		Collections: []string{" payment_routes "}, MinRevision: 8,
	})
	if err != nil || len(response.Collections) != 1 || response.Collections[0].Name != "payment_routes" || response.Collections[0].Revision != 8 {
		t.Fatalf("collections response = %+v, %v", response, err)
	}
	_, err = service.GetCollections(context.Background(), configserver.GetCollectionsRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production", Collections: []string{"legacy"},
	})
	if !errors.Is(err, configserver.ErrCollectionForbidden) {
		t.Fatalf("unauthorized collection error = %v", err)
	}
	_, err = service.GetCollections(context.Background(), configserver.GetCollectionsRequest{
		ConsumerID: "payment-service", ClientID: "pod-1", Region: "cn", Environment: "production",
		Collections: []string{"payment_routes"}, MinRevision: 9,
	})
	if !errors.Is(err, configserver.ErrSnapshotUnavailable) || len(submitter.targets) != 1 || submitter.targets[0].Collection != "payment_routes" || submitter.targets[0].MinRevision != 9 {
		t.Fatalf("minimum wait error=%v targets=%+v", err, submitter.targets)
	}
}

func TestGetSnapshotReportsUnavailableBeforeInitialSnapshot(t *testing.T) {
	t.Parallel()
	service := configserver.New(emptySnapshotProvider{}, staticAuthorizer{}, "production")
	_, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod", Region: "cn", Environment: "production",
	})
	if !errors.Is(err, configserver.ErrSnapshotUnavailable) {
		t.Fatalf("missing snapshot error = %v", err)
	}
}

func TestGetSnapshotEnforcesSubscriptionAndEnvironment(t *testing.T) {
	t.Parallel()

	manager, _ := serverSnapshot(t)
	service := configserver.New(manager, staticAuthorizer{}, "production")
	response, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{ConsumerID: "other", ClientID: "pod", Region: "cn", Environment: "production", KnownVersions: []configserver.Version{{Collection: "old", Revision: 1, Digest: "digest"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Collections) != 0 || len(response.DeletedCollections) != 1 || response.DeletedCollections[0] != "old" {
		t.Fatalf("unauthorized response = %+v", response)
	}
	if _, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{ConsumerID: "other", ClientID: "pod", Region: "cn", Environment: "staging"}); err == nil {
		t.Fatal("environment mismatch succeeded")
	}
}

func TestGetSnapshotRejectsSnapshotOutsideManagedEnvironment(t *testing.T) {
	t.Parallel()
	manager, err := snapshot.NewManager(serverSource{}, snapshot.IdentitySeed{
		ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "wrong-environment",
	}, serverClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "staging"); err != nil {
		t.Fatal(err)
	}
	service := configserver.New(manager, staticAuthorizer{}, "production")
	if _, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod", Region: "cn", Environment: "staging",
	}); err == nil {
		t.Fatal("snapshot outside the managed environment was served")
	}
}

func TestGetSnapshotSelectsPercentageRuleByStableClientBucket(t *testing.T) {
	t.Parallel()
	manager, key := rolloutServerSnapshot(t)
	service := configserver.New(manager, staticAuthorizer{collections: []string{"payment_routes"}}, "production")

	selected, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod-10", Region: "cn", Environment: "production", Stage: "blue",
	})
	if err != nil {
		t.Fatal(err)
	}
	unselected, err := service.GetSnapshot(context.Background(), configserver.GetSnapshotRequest{
		ConsumerID: "payment-service", ClientID: "pod-8", Region: "cn", Environment: "production", Stage: "blue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Collections[0].Records[0].RecordKey != key || selected.Collections[0].Records[0].Data["priority"] != "9" {
		t.Fatalf("selected bucket snapshot = %+v", selected)
	}
	if unselected.Collections[0].Records[0].Data["priority"] != "1" {
		t.Fatalf("unselected bucket snapshot = %+v", unselected)
	}
	if selected.Collections[0].Digest == unselected.Collections[0].Digest {
		t.Fatal("distinct effective snapshots shared one digest")
	}
}

type staticAuthorizer struct{ collections []string }

func (authorizer staticAuthorizer) AuthorizedCollections(context.Context, string) ([]string, error) {
	return authorizer.collections, nil
}

type emptySnapshotProvider struct{}

func (emptySnapshotProvider) Current() *snapshot.Snapshot { return nil }

type serverSource struct{ input []snapshot.CollectionInput }

func (source serverSource) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	return source.input, nil
}

type serverClock struct{}

func (serverClock) Now() time.Time { return time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC) }

type recordingRefreshSubmitter struct{ targets []snapshot.RefreshTarget }

func (submitter *recordingRefreshSubmitter) Submit(targets []snapshot.RefreshTarget) error {
	submitter.targets = append([]snapshot.RefreshTarget(nil), targets...)
	return nil
}

func serverSnapshot(t *testing.T) (*snapshot.Manager, string) {
	t.Helper()
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "payment_routes", SDKDeliveryEnabled: true, SchemaVersion: 1, KeyFields: []string{"route_code"},
		Fields: []catalog.FieldDefinition{
			{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := definition.NewRecord("production", map[string]string{"route_code": "visa-cn", "priority": "7"})
	if err != nil {
		t.Fatal(err)
	}
	record.ConfigRevision = 8
	manager, err := snapshot.NewManager(serverSource{input: []snapshot.CollectionInput{{Definition: definition, Version: 8, Cursor: 31, Records: []catalog.ConfigurationRecord{record}}}}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, serverClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	return manager, record.RecordKey
}

func rolloutServerSnapshot(t *testing.T) (*snapshot.Manager, string) {
	t.Helper()
	definition, err := catalog.CompileCollection(catalog.CollectionSpec{
		Name: "payment_routes", SDKDeliveryEnabled: true, SchemaVersion: 1, KeyFields: []string{"route_code"},
		Fields: []catalog.FieldDefinition{
			{Name: "route_code", DisplayName: "Route code", Type: catalog.FieldTypeString, Required: true, DisplayOrder: 0},
			{Name: "priority", DisplayName: "Priority", Type: catalog.FieldTypeInt64, Required: true, DisplayOrder: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := definition.NewRecord("production", map[string]string{"route_code": "visa", "priority": "1"})
	if err != nil {
		t.Fatal(err)
	}
	base.ConfigRevision = 7
	activation := catalog.ConfigRevision(8)
	rule := overlay.Rule{
		ID: "rollout", Collection: definition.Name(), Scope: overlay.Scope{Region: "cn", Environment: "production", Stage: "blue"},
		RecordKey: base.RecordKey, Action: overlay.ActionModify, Content: map[string]string{"route_code": "visa", "priority": "9"},
		RolloutRanges: []overlay.BucketRange{{Start: 0, End: 9}}, ConfigRevision: 8, ReleaseOrderID: "order", ActivatedRevision: &activation,
	}
	manager, err := snapshot.NewManager(serverSource{input: []snapshot.CollectionInput{{
		Definition: definition, Version: 8, Records: []catalog.ConfigurationRecord{base}, OverlayRules: []overlay.Rule{rule},
	}}}, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "rollout"}, serverClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	return manager, base.RecordKey
}
