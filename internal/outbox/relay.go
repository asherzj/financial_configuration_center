package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type LeaseRevision uint64

type Status string

const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusSent       Status = "SENT"
	StatusDeadLetter Status = "DEAD_LETTER"
)

var ErrLeaseLost = errors.New("outbox lease was lost")

type Event struct {
	ID             string
	Sequence       uint64
	AggregateType  string
	AggregateID    string
	Type           string
	PayloadVersion uint32
	Payload        []byte
	IdempotencyKey string
	Status         Status
	LeaseRevision  LeaseRevision
	Attempts       int
	LockedBy       string
	LockedUntil    time.Time
}

type ClaimRequest struct {
	WorkerID      string
	Limit         int
	Now           time.Time
	LeaseDuration time.Duration
}

type Repository interface {
	Claim(context.Context, ClaimRequest) ([]Event, error)
	MarkSent(context.Context, Event, time.Time) error
	MarkFailed(context.Context, Event, string, time.Time, int, time.Time) (Status, error)
}

type Sender interface {
	Send(context.Context, Event) error
}

type Clock interface {
	Now() time.Time
}

type RelayOptions struct {
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	MaxAttempts   int
	RetryDelay    time.Duration
}

type RunResult struct {
	Claimed    int
	Sent       int
	Retried    int
	DeadLetter int
}

type Relay struct {
	repository Repository
	sender     Sender
	options    RelayOptions
	clock      Clock
}

func NewRelay(repository Repository, sender Sender, options RelayOptions, clock Clock) (*Relay, error) {
	if repository == nil || sender == nil || clock == nil {
		return nil, errors.New("outbox relay dependencies are required")
	}
	if strings.TrimSpace(options.WorkerID) == "" || options.BatchSize <= 0 || options.BatchSize > 100 || options.LeaseDuration <= 0 || options.MaxAttempts <= 0 || options.RetryDelay <= 0 {
		return nil, errors.New("outbox relay options are invalid")
	}
	return &Relay{repository: repository, sender: sender, options: options, clock: clock}, nil
}

func (relay *Relay) RunOnce(ctx context.Context) (RunResult, error) {
	now := relay.clock.Now().UTC()
	events, err := relay.repository.Claim(ctx, ClaimRequest{WorkerID: relay.options.WorkerID, Limit: relay.options.BatchSize, Now: now, LeaseDuration: relay.options.LeaseDuration})
	if err != nil {
		return RunResult{}, fmt.Errorf("claim outbox events: %w", err)
	}
	result := RunResult{Claimed: len(events)}
	var persistenceErrors []error
	for _, event := range events {
		if err := relay.sender.Send(ctx, event); err == nil {
			if err := relay.repository.MarkSent(ctx, event, relay.clock.Now().UTC()); err != nil {
				persistenceErrors = append(persistenceErrors, fmt.Errorf("mark outbox event %s sent: %w", event.ID, err))
				continue
			}
			result.Sent++
			continue
		}
		failedAt := relay.clock.Now().UTC()
		status, markErr := relay.repository.MarkFailed(ctx, event, "delivery failed", failedAt.Add(relay.options.RetryDelay), relay.options.MaxAttempts, failedAt)
		if markErr != nil {
			persistenceErrors = append(persistenceErrors, fmt.Errorf("mark outbox event %s failed: %w", event.ID, markErr))
			continue
		}
		if status == StatusDeadLetter {
			result.DeadLetter++
		} else {
			result.Retried++
		}
	}
	return result, errors.Join(persistenceErrors...)
}
