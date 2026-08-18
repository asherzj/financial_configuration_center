package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/outbox"
)

func TestRelayDeliversOutsideClaimAndCompletesByLeaseCAS(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	repository := &repositoryStub{claimed: []outbox.Event{{ID: "event-1", Type: "CONFIGURATION_CHANGED", Payload: []byte(`{"schemaVersion":1}`), Status: outbox.StatusProcessing, LeaseRevision: 2, Attempts: 1, LockedBy: "relay-a"}}}
	sender := &senderStub{claimReturned: &repository.claimReturned}
	relay, err := outbox.NewRelay(repository, sender, outbox.RelayOptions{WorkerID: "relay-a", BatchSize: 10, LeaseDuration: 30 * time.Second, MaxAttempts: 3, RetryDelay: time.Second}, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 || result.Sent != 1 || result.Retried != 0 || len(sender.events) != 1 || repository.sentRevision != 2 {
		t.Fatalf("result=%+v sender=%d sentRevision=%d", result, len(sender.events), repository.sentRevision)
	}
}

func TestRelaySchedulesFailureWithoutLosingOriginalLease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	repository := &repositoryStub{claimed: []outbox.Event{{ID: "event-1", Status: outbox.StatusProcessing, LeaseRevision: 8, Attempts: 2, LockedBy: "relay-a"}}}
	relay, err := outbox.NewRelay(repository, &senderStub{err: errors.New("endpoint unavailable")}, outbox.RelayOptions{WorkerID: "relay-a", BatchSize: 1, LeaseDuration: 30 * time.Second, MaxAttempts: 3, RetryDelay: 5 * time.Second}, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Retried != 1 || repository.failedRevision != 8 || repository.nextAttemptAt != now.Add(5*time.Second) {
		t.Fatalf("result=%+v failedRevision=%d next=%s", result, repository.failedRevision, repository.nextAttemptAt)
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type senderStub struct {
	events        []outbox.Event
	err           error
	claimReturned *bool
}

func (sender *senderStub) Send(_ context.Context, event outbox.Event) error {
	if sender.claimReturned != nil && !*sender.claimReturned {
		return errors.New("sender ran before claim returned")
	}
	sender.events = append(sender.events, event)
	return sender.err
}

type repositoryStub struct {
	claimed        []outbox.Event
	claimReturned  bool
	sentRevision   outbox.LeaseRevision
	failedRevision outbox.LeaseRevision
	nextAttemptAt  time.Time
}

func (repository *repositoryStub) Claim(_ context.Context, _ outbox.ClaimRequest) ([]outbox.Event, error) {
	claimed := append([]outbox.Event(nil), repository.claimed...)
	repository.claimReturned = true
	return claimed, nil
}

func (repository *repositoryStub) MarkSent(_ context.Context, event outbox.Event, _ time.Time) error {
	repository.sentRevision = event.LeaseRevision
	return nil
}

func (repository *repositoryStub) MarkFailed(_ context.Context, event outbox.Event, _ string, nextAttemptAt time.Time, _ int, _ time.Time) (outbox.Status, error) {
	repository.failedRevision = event.LeaseRevision
	repository.nextAttemptAt = nextAttemptAt
	return outbox.StatusPending, nil
}

func (repository *repositoryStub) Replay(context.Context, outbox.ReplayRequest) (outbox.Event, error) {
	return outbox.Event{}, nil
}
