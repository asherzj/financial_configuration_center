package snapshot_test

import (
	"errors"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

func TestHintReceiverDeduplicatesAndSubmitsWatermarks(t *testing.T) {
	t.Parallel()
	submitter := &recordingTargetSubmitter{}
	receiver, err := snapshot.NewHintReceiver(submitter, snapshot.HintReceiverOptions{
		ManagedEnvironment: "production", CacheSize: 2, DedupTTL: time.Minute,
	}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	hint := snapshot.RefreshHint{
		EventID: "event-1", Environment: "production",
		Targets: []snapshot.HintTarget{{Collection: " payment_routes ", MinRevision: 8, TargetCursor: 21}},
	}
	if err := receiver.Notify(hint); err != nil {
		t.Fatal(err)
	}
	if err := receiver.Notify(hint); err != nil {
		t.Fatalf("duplicate hint: %v", err)
	}
	if submitter.calls != 1 || len(submitter.targets) != 1 || submitter.targets[0].Collection != "payment_routes" ||
		submitter.targets[0].MinRevision != 8 || submitter.targets[0].TargetCursor != 21 {
		t.Fatalf("submitted targets calls=%d targets=%+v", submitter.calls, submitter.targets)
	}
}

func TestHintReceiverMapsCapacityAndDoesNotDeduplicateRejectedEvent(t *testing.T) {
	t.Parallel()
	submitter := &recordingTargetSubmitter{err: snapshot.ErrRefreshQueueFull}
	receiver, err := snapshot.NewHintReceiver(submitter, snapshot.HintReceiverOptions{
		ManagedEnvironment: "production", CacheSize: 2, DedupTTL: time.Minute,
	}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	hint := snapshot.RefreshHint{
		EventID: "event-1", Environment: "production",
		Targets: []snapshot.HintTarget{{Collection: "routes", MinRevision: 8}},
	}
	if err := receiver.Notify(hint); !errors.Is(err, snapshot.ErrHintQueueFull) {
		t.Fatalf("queue error = %v", err)
	}
	submitter.err = nil
	if err := receiver.Notify(hint); err != nil {
		t.Fatalf("retry rejected event: %v", err)
	}
	if submitter.calls != 2 {
		t.Fatalf("submit calls = %d", submitter.calls)
	}
}

func TestHintReceiverRejectsAnotherManagedEnvironment(t *testing.T) {
	t.Parallel()
	submitter := &recordingTargetSubmitter{}
	receiver, err := snapshot.NewHintReceiver(submitter, snapshot.HintReceiverOptions{
		ManagedEnvironment: "production", CacheSize: 2, DedupTTL: time.Minute,
	}, pollClock{})
	if err != nil {
		t.Fatal(err)
	}
	err = receiver.Notify(snapshot.RefreshHint{
		EventID: "wrong-environment", Environment: "staging",
		Targets: []snapshot.HintTarget{{Collection: "payment_routes", MinRevision: 8}},
	})
	if !errors.Is(err, snapshot.ErrManagedEnvironmentMismatch) || submitter.calls != 0 {
		t.Fatalf("cross-environment hint error=%v calls=%d", err, submitter.calls)
	}
}

type recordingTargetSubmitter struct {
	calls   int
	targets []snapshot.RefreshTarget
	err     error
}

func (submitter *recordingTargetSubmitter) Submit(targets []snapshot.RefreshTarget) error {
	submitter.calls++
	submitter.targets = append([]snapshot.RefreshTarget(nil), targets...)
	return submitter.err
}
