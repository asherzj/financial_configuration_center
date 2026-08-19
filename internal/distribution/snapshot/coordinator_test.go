package snapshot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
)

func TestRefreshCoordinatorMergesMaximumTargetsAndSkipsReachedWatermarks(t *testing.T) {
	t.Parallel()
	refresher := newCoordinatorRefresher(map[string]RefreshTarget{
		"routes": {Collection: "routes", MinRevision: 7, TargetCursor: 70},
	})
	coordinator, err := NewRefreshCoordinator(refresher, RefreshCoordinatorOptions{
		ManagedEnvironment: "production", MaxPendingCollections: 2,
		InitialBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit([]RefreshTarget{{Collection: " routes ", MinRevision: 8, TargetCursor: 80}}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit([]RefreshTarget{{Collection: "routes", MinRevision: 7, TargetCursor: 75}}); err != nil {
		t.Fatal(err)
	}
	refresher.next = map[string]RefreshTarget{"routes": {Collection: "routes", MinRevision: 8, TargetCursor: 80}}
	worked, err := coordinator.processPending(context.Background())
	if err != nil || !worked {
		t.Fatalf("process merged target = %t, %v", worked, err)
	}
	if refresher.callCount() != 1 || coordinator.hasPending() {
		t.Fatalf("refresh calls=%d pending=%v", refresher.callCount(), coordinator.hasPending())
	}

	if err := coordinator.Submit([]RefreshTarget{{Collection: "routes", MinRevision: 8, TargetCursor: 80}}); err != nil {
		t.Fatal(err)
	}
	worked, err = coordinator.processPending(context.Background())
	if err != nil || worked || refresher.callCount() != 1 {
		t.Fatalf("reached target = %t calls=%d err=%v", worked, refresher.callCount(), err)
	}
}

func TestRefreshCoordinatorRetainsFailedAndNewerTargets(t *testing.T) {
	t.Parallel()
	refresher := newCoordinatorRefresher(map[string]RefreshTarget{
		"routes": {Collection: "routes", MinRevision: 7, TargetCursor: 70},
	})
	refresher.errs = []error{errors.New("mysql unavailable")}
	coordinator := mustCoordinator(t, refresher, 2)
	if err := coordinator.Submit([]RefreshTarget{{Collection: "routes", MinRevision: 8, TargetCursor: 80}}); err != nil {
		t.Fatal(err)
	}
	if worked, err := coordinator.processPending(context.Background()); !worked || err == nil || !coordinator.hasPending() {
		t.Fatalf("failed target worked=%v pending=%v err=%v", worked, coordinator.hasPending(), err)
	}

	refresher.started = make(chan struct{})
	refresher.release = make(chan struct{})
	refresher.next = map[string]RefreshTarget{"routes": {Collection: "routes", MinRevision: 8, TargetCursor: 80}}
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.processPending(context.Background())
		done <- err
	}()
	<-refresher.started
	if err := coordinator.Submit([]RefreshTarget{{Collection: "routes", MinRevision: 9, TargetCursor: 90}}); err != nil {
		t.Fatal(err)
	}
	close(refresher.release)
	if err := <-done; !errors.Is(err, ErrRefreshTargetNotReached) || !coordinator.hasPending() {
		t.Fatalf("newer target err=%v pending=%v", err, coordinator.hasPending())
	}

	refresher.next = map[string]RefreshTarget{"routes": {Collection: "routes", MinRevision: 9, TargetCursor: 90}}
	refresher.started, refresher.release = nil, nil
	if worked, err := coordinator.processPending(context.Background()); err != nil || !worked || coordinator.hasPending() {
		t.Fatalf("retry target worked=%v pending=%v err=%v", worked, coordinator.hasPending(), err)
	}
}

func TestRefreshCoordinatorEnforcesAtomicCapacityAndBoundedBackoff(t *testing.T) {
	t.Parallel()
	refresher := newCoordinatorRefresher(nil)
	coordinator := mustCoordinator(t, refresher, 2)
	if err := coordinator.Submit([]RefreshTarget{
		{Collection: "routes", MinRevision: 1}, {Collection: "providers", TargetCursor: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit([]RefreshTarget{
		{Collection: "routes", MinRevision: 9}, {Collection: "flags", MinRevision: 1},
	}); !errors.Is(err, ErrRefreshQueueFull) {
		t.Fatalf("capacity error = %v", err)
	}
	coordinator.mu.Lock()
	routes := coordinator.pending["routes"]
	coordinator.mu.Unlock()
	if routes.MinRevision != 1 {
		t.Fatalf("rejected batch partially merged routes=%+v", routes)
	}
	if got := coordinator.retryDelay(0); got != time.Millisecond {
		t.Fatalf("initial backoff = %s", got)
	}
	if got := coordinator.retryDelay(1); got != 2*time.Millisecond {
		t.Fatalf("second backoff = %s", got)
	}
	if got := coordinator.retryDelay(8); got != 4*time.Millisecond {
		t.Fatalf("bounded backoff = %s", got)
	}
}

func TestRefreshCoordinatorRunRetriesAndStopsOnCancellation(t *testing.T) {
	refresher := newCoordinatorRefresher(nil)
	refresher.errs = []error{errors.New("temporary")}
	refresher.next = map[string]RefreshTarget{"routes": {Collection: "routes", MinRevision: 1, TargetCursor: 1}}
	coordinator := mustCoordinator(t, refresher, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	if err := coordinator.Submit([]RefreshTarget{{Collection: "routes", MinRevision: 1, TargetCursor: 1}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for refresher.callCount() < 2 {
		select {
		case <-deadline:
			t.Fatal("coordinator did not retry")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run cancellation = %v", err)
	}
}

func TestRefreshCoordinatorCoalescesForcedRefreshAndRejectsSecondRunLoop(t *testing.T) {
	t.Parallel()
	refresher := newCoordinatorRefresher(nil)
	coordinator := mustCoordinator(t, refresher, 1)
	if err := coordinator.RequestRefresh(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RequestRefresh(); err != nil {
		t.Fatal(err)
	}
	if worked, err := coordinator.processPending(context.Background()); err != nil || !worked || refresher.callCount() != 1 || coordinator.hasPending() {
		t.Fatalf("forced refresh worked=%v calls=%d pending=%v err=%v", worked, refresher.callCount(), coordinator.hasPending(), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.runMu.Lock()
		running := coordinator.running
		coordinator.runMu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("coordinator Run did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := coordinator.Run(context.Background()); !errors.Is(err, ErrRefreshCoordinatorRun) {
		t.Fatalf("second Run error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("first Run cancellation = %v", err)
	}
}

func TestRefreshCoordinatorRejectsInvalidConstructionAndTargets(t *testing.T) {
	t.Parallel()
	if _, err := NewRefreshCoordinator(nil, RefreshCoordinatorOptions{}); err == nil {
		t.Fatal("invalid coordinator construction succeeded")
	}
	coordinator := mustCoordinator(t, newCoordinatorRefresher(nil), 1)
	for _, targets := range [][]RefreshTarget{
		nil,
		{{Collection: "routes"}},
		{{Collection: " ", MinRevision: 1}},
	} {
		if err := coordinator.Submit(targets); err == nil {
			t.Fatalf("invalid targets succeeded: %+v", targets)
		}
	}
}

func mustCoordinator(t *testing.T, refresher SnapshotRefresher, capacity int) *RefreshCoordinator {
	t.Helper()
	coordinator, err := NewRefreshCoordinator(refresher, RefreshCoordinatorOptions{
		ManagedEnvironment: "production", MaxPendingCollections: capacity,
		InitialBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

type coordinatorRefresher struct {
	mu      sync.Mutex
	current *Snapshot
	next    map[string]RefreshTarget
	errs    []error
	calls   int
	started chan struct{}
	release chan struct{}
}

func newCoordinatorRefresher(watermarks map[string]RefreshTarget) *coordinatorRefresher {
	return &coordinatorRefresher{current: coordinatorSnapshot(watermarks)}
}

func (refresher *coordinatorRefresher) Current() *Snapshot {
	refresher.mu.Lock()
	defer refresher.mu.Unlock()
	return refresher.current
}

func (refresher *coordinatorRefresher) Refresh(ctx context.Context, environment string) (RefreshResult, error) {
	refresher.mu.Lock()
	refresher.calls++
	var resultErr error
	if len(refresher.errs) > 0 {
		resultErr = refresher.errs[0]
		refresher.errs = refresher.errs[1:]
	}
	started, release := refresher.started, refresher.release
	next := refresher.next
	refresher.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return RefreshResult{}, ctx.Err()
		case <-release:
		}
	}
	if resultErr != nil {
		return RefreshResult{}, resultErr
	}
	refresher.mu.Lock()
	refresher.current = coordinatorSnapshot(next)
	generation := refresher.current.identity.Generation
	refresher.mu.Unlock()
	return RefreshResult{Generation: generation, CollectionCount: len(next)}, nil
}

func (refresher *coordinatorRefresher) callCount() int {
	refresher.mu.Lock()
	defer refresher.mu.Unlock()
	return refresher.calls
}

func coordinatorSnapshot(watermarks map[string]RefreshTarget) *Snapshot {
	views := make(map[string]collectionView, len(watermarks))
	for name, watermark := range watermarks {
		views[name] = collectionView{version: watermark.MinRevision, cursor: watermark.TargetCursor}
	}
	return &Snapshot{
		identity: Identity{Generation: 1}, environment: "production", collections: views,
		models: map[string]readmodel.CompiledModel{},
	}
}
