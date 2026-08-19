package snapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	readmodel "github.com/asherzj/financial_configuration_center/internal/distribution/readmodel"
)

var (
	ErrRefreshQueueFull        = errors.New("refresh coordinator capacity is full")
	ErrRefreshTargetNotReached = errors.New("refresh target was not reached")
	ErrRefreshCoordinatorRun   = errors.New("refresh coordinator is already running")
)

type RefreshTarget struct {
	Collection   string
	MinRevision  readmodel.ConfigRevision
	TargetCursor uint64
}

type RefreshCoordinatorOptions struct {
	ManagedEnvironment    string
	MaxPendingCollections int
	InitialBackoff        time.Duration
	MaxBackoff            time.Duration
}

// RefreshCoordinator is the single owner of durable in-process refresh
// watermarks. Submitters only merge targets and wake its one writer loop.
type RefreshCoordinator struct {
	refresher   SnapshotRefresher
	environment string
	options     RefreshCoordinatorOptions
	wake        chan struct{}

	mu             sync.Mutex
	pending        map[string]RefreshTarget
	forceRequested uint64
	forceCompleted uint64

	runMu   sync.Mutex
	running bool
}

func NewRefreshCoordinator(refresher SnapshotRefresher, options RefreshCoordinatorOptions) (*RefreshCoordinator, error) {
	options.ManagedEnvironment = strings.TrimSpace(options.ManagedEnvironment)
	if refresher == nil || options.ManagedEnvironment == "" || options.MaxPendingCollections <= 0 ||
		options.InitialBackoff <= 0 || options.MaxBackoff < options.InitialBackoff {
		return nil, errors.New("new refresh coordinator: refresher, managed environment, capacity, and valid backoff are required")
	}
	return &RefreshCoordinator{
		refresher: refresher, environment: options.ManagedEnvironment, options: options,
		wake: make(chan struct{}, 1), pending: make(map[string]RefreshTarget, options.MaxPendingCollections),
	}, nil
}

// Submit atomically merges maximum per-collection watermarks. A rejected batch
// never partially changes targets already owned by the coordinator.
func (coordinator *RefreshCoordinator) Submit(targets []RefreshTarget) error {
	if coordinator == nil {
		return errors.New("submit refresh targets: coordinator is required")
	}
	merged := make(map[string]RefreshTarget, len(targets))
	for _, target := range targets {
		target.Collection = strings.TrimSpace(target.Collection)
		if target.Collection == "" || (target.MinRevision == 0 && target.TargetCursor == 0) {
			return errors.New("submit refresh targets: collection and a positive watermark are required")
		}
		current := merged[target.Collection]
		current.Collection = target.Collection
		current.MinRevision = maxRevision(current.MinRevision, target.MinRevision)
		current.TargetCursor = max(current.TargetCursor, target.TargetCursor)
		merged[target.Collection] = current
	}
	if len(merged) == 0 {
		return errors.New("submit refresh targets: at least one target is required")
	}

	current := coordinator.refresher.Current()
	coordinator.mu.Lock()
	for collection, target := range merged {
		if targetReached(current, coordinator.environment, target) {
			delete(merged, collection)
		}
	}
	if len(merged) == 0 {
		coordinator.mu.Unlock()
		return nil
	}
	coordinator.clearReachedLocked(current)
	additional := 0
	for collection := range merged {
		if _, exists := coordinator.pending[collection]; !exists {
			additional++
		}
	}
	if len(coordinator.pending)+additional > coordinator.options.MaxPendingCollections {
		coordinator.mu.Unlock()
		return ErrRefreshQueueFull
	}
	for collection, target := range merged {
		current := coordinator.pending[collection]
		current.Collection = collection
		current.MinRevision = maxRevision(current.MinRevision, target.MinRevision)
		current.TargetCursor = max(current.TargetCursor, target.TargetCursor)
		coordinator.pending[collection] = current
	}
	coordinator.mu.Unlock()
	coordinator.signal()
	return nil
}

// RequestRefresh records a non-watermark refresh request, used when authority
// detects a deletion or another change that cannot be represented by a target.
func (coordinator *RefreshCoordinator) RequestRefresh() error {
	if coordinator == nil {
		return errors.New("request refresh: coordinator is required")
	}
	coordinator.mu.Lock()
	coordinator.forceRequested++
	coordinator.mu.Unlock()
	coordinator.signal()
	return nil
}

func (coordinator *RefreshCoordinator) Run(ctx context.Context) error {
	if coordinator == nil {
		return errors.New("run refresh coordinator: coordinator is required")
	}
	coordinator.runMu.Lock()
	if coordinator.running {
		coordinator.runMu.Unlock()
		return ErrRefreshCoordinatorRun
	}
	coordinator.running = true
	coordinator.runMu.Unlock()
	defer func() {
		coordinator.runMu.Lock()
		coordinator.running = false
		coordinator.runMu.Unlock()
	}()

	attempt := 0
	for {
		if !coordinator.hasPending() {
			select {
			case <-ctx.Done():
				return nil
			case <-coordinator.wake:
			}
		}
		worked, err := coordinator.processPending(ctx)
		if err == nil {
			attempt = 0
			if !worked {
				continue
			}
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if ctx.Err() != nil {
				return nil
			}
		}
		timer := time.NewTimer(coordinator.retryDelay(attempt))
		attempt++
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func (coordinator *RefreshCoordinator) processPending(ctx context.Context) (bool, error) {
	current := coordinator.refresher.Current()
	coordinator.mu.Lock()
	coordinator.clearReachedLocked(current)
	force := coordinator.forceRequested
	if len(coordinator.pending) == 0 && force == coordinator.forceCompleted {
		coordinator.mu.Unlock()
		return false, nil
	}
	coordinator.mu.Unlock()

	if _, err := coordinator.refresher.Refresh(ctx, coordinator.environment); err != nil {
		return true, fmt.Errorf("coordinate snapshot refresh: %w", err)
	}
	current = coordinator.refresher.Current()
	coordinator.mu.Lock()
	coordinator.clearReachedLocked(current)
	if force > coordinator.forceCompleted {
		coordinator.forceCompleted = force
	}
	unreached := len(coordinator.pending)
	coordinator.mu.Unlock()
	if unreached > 0 {
		return true, ErrRefreshTargetNotReached
	}
	return true, nil
}

func (coordinator *RefreshCoordinator) clearReachedLocked(current *Snapshot) {
	for collection, target := range coordinator.pending {
		if targetReached(current, coordinator.environment, target) {
			delete(coordinator.pending, collection)
		}
	}
}

func targetReached(current *Snapshot, environment string, target RefreshTarget) bool {
	if current == nil || current.Environment() != environment {
		return false
	}
	revision, revisionExists := current.CollectionVersion(target.Collection)
	cursor, cursorExists := current.CollectionCursor(target.Collection)
	return revisionExists && revision >= target.MinRevision && cursorExists && cursor >= target.TargetCursor
}

func (coordinator *RefreshCoordinator) hasPending() bool {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return len(coordinator.pending) > 0 || coordinator.forceRequested > coordinator.forceCompleted
}

func (coordinator *RefreshCoordinator) signal() {
	select {
	case coordinator.wake <- struct{}{}:
	default:
	}
}

func (coordinator *RefreshCoordinator) retryDelay(attempt int) time.Duration {
	delay := coordinator.options.InitialBackoff
	for step := 0; step < attempt && delay < coordinator.options.MaxBackoff; step++ {
		if delay > coordinator.options.MaxBackoff/2 {
			return coordinator.options.MaxBackoff
		}
		delay *= 2
	}
	if delay > coordinator.options.MaxBackoff {
		return coordinator.options.MaxBackoff
	}
	return delay
}

func maxRevision(left, right readmodel.ConfigRevision) readmodel.ConfigRevision {
	if right > left {
		return right
	}
	return left
}
