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
	scheduler := &recordingRefreshScheduler{}
	poller, err := snapshot.NewVersionPoller(manager, source, scheduler, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := poller.PollOnce(context.Background()); err != nil || result.Submitted || manager.Current().Identity().Generation != 1 || scheduler.submitCalls != 0 {
		t.Fatalf("unchanged poll = %+v, %v", result, err)
	}

	record.Data["priority"] = "2"
	record.ConfigRevision = 8
	source.set(map[string]catalog.ConfigRevision{"payment_routes": 8}, []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 8, Records: []catalog.ConfigurationRecord{record}}})
	result, err := poller.PollOnce(context.Background())
	if err != nil || !result.Submitted || result.Generation != 1 {
		t.Fatalf("changed poll = %+v, %v", result, err)
	}
	if scheduler.submitCalls != 1 || len(scheduler.targets) != 1 || scheduler.targets[0].Collection != "payment_routes" || scheduler.targets[0].MinRevision != 8 {
		t.Fatalf("scheduled targets calls=%d targets=%+v", scheduler.submitCalls, scheduler.targets)
	}
	got, _ := manager.Current().Record("payment_routes", record.RecordKey)
	if got.Data["priority"] != "1" || got.ConfigRevision != 7 || manager.Current().Identity().Generation != 1 {
		t.Fatalf("poller bypassed coordinator: record=%+v generation=%d", got, manager.Current().Identity().Generation)
	}
}

func TestVersionPollerRequestsRefreshWhenAuthorityRemovesCollection(t *testing.T) {
	t.Parallel()
	definition, model := snapshotCatalog(t)
	source := &pollSource{
		versions: map[string]catalog.ConfigRevision{"payment_routes": 7},
		inputs:   []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 7}},
	}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	scheduler := &recordingRefreshScheduler{}
	poller, err := snapshot.NewVersionPoller(manager, source, scheduler, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.set(map[string]catalog.ConfigRevision{}, nil)
	result, err := poller.PollOnce(context.Background())
	if err != nil || !result.Submitted || scheduler.forceCalls != 1 || scheduler.submitCalls != 0 {
		t.Fatalf("removed collection result=%+v force=%d submits=%d err=%v", result, scheduler.forceCalls, scheduler.submitCalls, err)
	}
}

func TestVersionPollerFallsBackToForcedRefreshWhenTargetCapacityIsFull(t *testing.T) {
	t.Parallel()
	definition, model := snapshotCatalog(t)
	source := &pollSource{
		versions: map[string]catalog.ConfigRevision{"payment_routes": 7},
		inputs:   []snapshot.CollectionInput{{Definition: definition, Models: []catalog.CompiledModel{model}, Version: 7}},
	}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	scheduler := &recordingRefreshScheduler{submitErr: snapshot.ErrRefreshQueueFull}
	poller, err := snapshot.NewVersionPoller(manager, source, scheduler, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.set(map[string]catalog.ConfigRevision{"payment_routes": 8}, source.inputs)
	result, err := poller.PollOnce(context.Background())
	if err != nil || !result.Submitted || scheduler.submitCalls != 1 || scheduler.forceCalls != 1 {
		t.Fatalf("capacity fallback result=%+v submits=%d force=%d err=%v", result, scheduler.submitCalls, scheduler.forceCalls, err)
	}
}

func TestVersionPollerRunHandlesMissingInitialSnapshot(t *testing.T) {
	t.Parallel()
	manager := nilSnapshotRefresher{}
	source := &pollSource{versions: map[string]catalog.ConfigRevision{"routes": 1}}
	scheduler := &recordingRefreshScheduler{}
	poller, err := snapshot.NewVersionPoller(manager, source, scheduler, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run with missing snapshot = %v", err)
	}
}

func TestVersionPollerUsesCapacityIndependentForceBeforeInitialSnapshot(t *testing.T) {
	t.Parallel()
	manager := nilSnapshotRefresher{}
	source := &pollSource{versions: map[string]catalog.ConfigRevision{"routes": 1, "providers": 2}}
	scheduler := &recordingRefreshScheduler{}
	poller, err := snapshot.NewVersionPoller(manager, source, scheduler, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := poller.PollOnce(context.Background())
	if err != nil || !result.Submitted || scheduler.forceCalls != 1 || scheduler.submitCalls != 0 {
		t.Fatalf("initial poll result=%+v force=%d submits=%d err=%v", result, scheduler.forceCalls, scheduler.submitCalls, err)
	}
}

func TestVersionPollerTreatsInitializedAuthoritativeEmptySnapshotAsNoOp(t *testing.T) {
	t.Parallel()
	source := &pollSource{versions: map[string]catalog.ConfigRevision{}}
	manager, err := snapshot.NewManager(source, snapshot.IdentitySeed{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance"}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), "production"); err != nil {
		t.Fatal(err)
	}
	scheduler := &recordingRefreshScheduler{}
	poller, err := snapshot.NewVersionPoller(manager, source, scheduler, snapshot.VersionPollerOptions{Environment: "production", Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := poller.PollOnce(context.Background())
	if err != nil || result.Submitted || scheduler.forceCalls != 0 || scheduler.submitCalls != 0 || manager.Current().Identity().Generation != 1 {
		t.Fatalf("authoritative empty result=%+v force=%d submits=%d generation=%d err=%v", result, scheduler.forceCalls, scheduler.submitCalls, manager.Current().Identity().Generation, err)
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

type recordingRefreshScheduler struct {
	submitCalls int
	forceCalls  int
	targets     []snapshot.RefreshTarget
	submitErr   error
	forceErr    error
}

func (scheduler *recordingRefreshScheduler) Submit(targets []snapshot.RefreshTarget) error {
	scheduler.submitCalls++
	scheduler.targets = append([]snapshot.RefreshTarget(nil), targets...)
	return scheduler.submitErr
}

func (scheduler *recordingRefreshScheduler) RequestRefresh() error {
	scheduler.forceCalls++
	return scheduler.forceErr
}

type nilSnapshotRefresher struct{}

func (nilSnapshotRefresher) Current() *snapshot.Snapshot { return nil }

func (nilSnapshotRefresher) Refresh(context.Context, string) (snapshot.RefreshResult, error) {
	return snapshot.RefreshResult{}, nil
}
