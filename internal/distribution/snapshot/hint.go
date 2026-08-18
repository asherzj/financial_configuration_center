package snapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

var ErrHintQueueFull = errors.New("refresh hint queue is full")

type HintTarget struct {
	Collection   string
	MinRevision  catalog.ConfigRevision
	TargetCursor uint64
}

type RefreshHint struct {
	EventID        string
	Environment    string
	Targets        []HintTarget
	ReleaseOrderID string
	TraceID        string
}

type HintReceiverOptions struct {
	QueueSize int
	CacheSize int
	DedupTTL  time.Duration
}

type HintReceiver struct {
	manager SnapshotRefresher
	clock   Clock
	options HintReceiverOptions
	queue   chan RefreshHint
	mu      sync.Mutex
	seen    map[string]time.Time
}

func NewHintReceiver(manager SnapshotRefresher, options HintReceiverOptions, clock Clock) (*HintReceiver, error) {
	if manager == nil || clock == nil || options.QueueSize <= 0 || options.CacheSize <= 0 || options.DedupTTL <= 0 {
		return nil, errors.New("new hint receiver: manager, clock, and positive limits are required")
	}
	return &HintReceiver{manager: manager, clock: clock, options: options, queue: make(chan RefreshHint, options.QueueSize), seen: make(map[string]time.Time, options.CacheSize)}, nil
}

func (receiver *HintReceiver) Notify(hint RefreshHint) error {
	hint.EventID = strings.TrimSpace(hint.EventID)
	hint.Environment = strings.TrimSpace(hint.Environment)
	if hint.EventID == "" || hint.Environment == "" || len(hint.Targets) == 0 {
		return errors.New("refresh hint event, environment, and targets are required")
	}
	for index := range hint.Targets {
		hint.Targets[index].Collection = strings.TrimSpace(hint.Targets[index].Collection)
		if hint.Targets[index].Collection == "" || hint.Targets[index].MinRevision == 0 {
			return errors.New("refresh hint targets require collection and positive revision")
		}
	}
	now := receiver.clock.Now().UTC()
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	receiver.pruneLocked(now)
	if expiresAt, duplicate := receiver.seen[hint.EventID]; duplicate && expiresAt.After(now) {
		return nil
	}
	if !hintNeeded(receiver.manager.Current(), hint) {
		receiver.rememberLocked(hint.EventID, now)
		return nil
	}
	select {
	case receiver.queue <- hint:
		receiver.rememberLocked(hint.EventID, now)
		return nil
	default:
		return ErrHintQueueFull
	}
}

func (receiver *HintReceiver) ProcessNext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case hint := <-receiver.queue:
		if !hintNeeded(receiver.manager.Current(), hint) {
			return nil
		}
		if _, err := receiver.manager.Refresh(ctx, hint.Environment); err != nil {
			return fmt.Errorf("process refresh hint %s: %w", hint.EventID, err)
		}
		return nil
	}
}

func (receiver *HintReceiver) Run(ctx context.Context) error {
	for {
		if err := receiver.ProcessNext(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
		}
	}
}

func hintNeeded(current *Snapshot, hint RefreshHint) bool {
	if current == nil || current.Environment() != hint.Environment {
		return true
	}
	for _, target := range hint.Targets {
		revision, exists := current.CollectionVersion(target.Collection)
		if !exists || revision < target.MinRevision {
			return true
		}
	}
	return false
}

func (receiver *HintReceiver) pruneLocked(now time.Time) {
	for eventID, expiresAt := range receiver.seen {
		if !expiresAt.After(now) {
			delete(receiver.seen, eventID)
		}
	}
}

func (receiver *HintReceiver) rememberLocked(eventID string, now time.Time) {
	if len(receiver.seen) >= receiver.options.CacheSize {
		var oldestID string
		var oldest time.Time
		for candidate, expiresAt := range receiver.seen {
			if oldestID == "" || expiresAt.Before(oldest) {
				oldestID, oldest = candidate, expiresAt
			}
		}
		delete(receiver.seen, oldestID)
	}
	receiver.seen[eventID] = now.Add(receiver.options.DedupTTL)
}
