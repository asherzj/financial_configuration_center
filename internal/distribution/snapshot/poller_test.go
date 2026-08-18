package snapshot_test

import (
	"context"
	"sync"
	"testing"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

func TestVersionPollerRefreshesOnlyWhenAuthorityMoves(t *testing.T) {
	t.Parallel()
	definition, model := snapshotCatalog(t)
	record, err := definition.NewRecord("production", map[string]string{"route_code": "visa", "priority": "1"})
	if err != nil {
		t.Fatal(err)
	}
	record.ConfigRevision = 7
	source := &pollSource{versions: map[string]catalog.ConfigRevision{"payment_routes": 7}, inputs: []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 7, Records: []catalog.ConfigurationRecord{record}}}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	poller, err := snapshot.NewVersionPoller(manager, source, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := poller.PollOnce(context.Background()); err != nil || result.Refreshed || manager.Current().Identity().Generation != 1 {
		t.Fatalf("unchanged poll = %+v, %v", result, err)
	}

	record.Data["priority"] = "2"
	record.ConfigRevision = 8
	source.set(map[string]catalog.ConfigRevision{"payment_routes": 8}, []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 8, Records: []catalog.ConfigurationRecord{record}}})
	result, err := poller.PollOnce(context.Background())
	if err != nil || !result.Refreshed || result.Generation != 2 {
		t.Fatalf("changed poll = %+v, %v", result, err)
	}
	got, _ := manager.Current().Record("payment_routes", record.RecordKey)
	if got.Data["priority"] != "2" || got.ConfigRevision != 8 {
		t.Fatalf("polled record = %+v", got)
	}
}

type pollClock struct{}

func (pollClock) Now() time.Time { return time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC) }

type pollSource struct {
	mu       sync.RWMutex
	versions map[string]catalog.ConfigRevision
	inputs   []snapshot.CollectionInput
}

func (source *pollSource) LoadEnvironment(context.Context, string) ([]snapshot.CollectionInput, error) {
	source.mu.RLock()
	defer source.mu.RUnlock()
	return append([]snapshot.CollectionInput(nil), source.inputs...), nil
}

func (source *pollSource) LoadVersions(context.Context, string) (map[string]catalog.ConfigRevision, error) {
	source.mu.RLock()
	defer source.mu.RUnlock()
	versions := make(map[string]catalog.ConfigRevision, len(source.versions))
	for name, revision := range source.versions {
		versions[name] = revision
	}
	return versions, nil
}

func (source *pollSource) set(versions map[string]catalog.ConfigRevision, inputs []snapshot.CollectionInput) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.versions = versions
	source.inputs = inputs
}
