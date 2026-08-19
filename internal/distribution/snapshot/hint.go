package snapshot

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

var (
	ErrHintQueueFull              = errors.New("refresh hint queue is full")
	ErrManagedEnvironmentMismatch = errors.New("refresh hint environment does not match the managed environment")
)

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
	ManagedEnvironment string
	CacheSize          int
	DedupTTL           time.Duration
}

type RefreshTargetSubmitter interface {
	Submit([]RefreshTarget) error
}

type HintReceiver struct {
	submitter          RefreshTargetSubmitter
	clock              Clock
	options            HintReceiverOptions
	managedEnvironment string
	mu                 sync.Mutex
	seen               map[string]time.Time
}

func NewHintReceiver(submitter RefreshTargetSubmitter, options HintReceiverOptions, clock Clock) (*HintReceiver, error) {
	options.ManagedEnvironment = strings.TrimSpace(options.ManagedEnvironment)
	if submitter == nil || clock == nil || options.ManagedEnvironment == "" || options.CacheSize <= 0 || options.DedupTTL <= 0 {
		return nil, errors.New("new hint receiver: submitter, managed environment, clock, and positive limits are required")
	}
	return &HintReceiver{
		submitter: submitter, clock: clock, options: options, managedEnvironment: options.ManagedEnvironment,
		seen: make(map[string]time.Time, options.CacheSize),
	}, nil
}

func (receiver *HintReceiver) Notify(hint RefreshHint) error {
	hint.EventID = strings.TrimSpace(hint.EventID)
	hint.Environment = strings.TrimSpace(hint.Environment)
	if hint.EventID == "" || hint.Environment == "" || len(hint.Targets) == 0 {
		return errors.New("refresh hint event, environment, and targets are required")
	}
	if hint.Environment != receiver.managedEnvironment {
		return fmt.Errorf("%w: got %q, want %q", ErrManagedEnvironmentMismatch, hint.Environment, receiver.managedEnvironment)
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
	targets := make([]RefreshTarget, len(hint.Targets))
	for index, target := range hint.Targets {
		targets[index] = RefreshTarget{
			Collection: target.Collection, MinRevision: target.MinRevision, TargetCursor: target.TargetCursor,
		}
	}
	if err := receiver.submitter.Submit(targets); err != nil {
		if errors.Is(err, ErrRefreshQueueFull) {
			return ErrHintQueueFull
		}
		return fmt.Errorf("submit refresh hint targets: %w", err)
	}
	receiver.rememberLocked(hint.EventID, now)
	return nil
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
