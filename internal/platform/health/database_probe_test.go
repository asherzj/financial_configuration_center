package health_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/health"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
)

var _ health.DatabasePinger = (*platformmysql.Database)(nil)

func TestDatabaseProbeTrackerKeepsReadinessThroughTransientFailure(t *testing.T) {
	t.Parallel()
	clock := &probeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}
	pinger := &probePinger{}
	tracker, err := health.NewDatabaseProbeTracker(pinger, time.Second, 100*time.Millisecond, 3*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("tracker without a successful probe was ready")
	}
	if err := tracker.ProbeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	pinger.SetError(errors.New("mysql unavailable"))
	if err := tracker.ProbeOnce(t.Context()); err == nil {
		t.Fatal("failed database probe reported success")
	}
	if err := tracker.Check(t.Context()); err != nil {
		t.Fatalf("transient failure cleared readiness inside grace: %v", err)
	}
	clock.Advance(2 * time.Second)
	if !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("stale last success remained ready beyond grace")
	}
	pinger.SetError(nil)
	if err := tracker.ProbeOnce(t.Context()); err != nil || tracker.Check(t.Context()) != nil {
		t.Fatalf("successful recovery did not restore readiness: %v", err)
	}
	if last, ok := tracker.LastSuccess(); !ok || !last.Equal(clock.Now()) {
		t.Fatalf("last success = %v, %v", last, ok)
	}
}

func TestDatabaseProbeTrackerDoesNotRecordLateSuccess(t *testing.T) {
	t.Parallel()
	tracker, err := health.NewDatabaseProbeTracker(probePingerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}), time.Second, 10*time.Millisecond, 2*time.Second, &probeClock{now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ProbeOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late probe result = %v", err)
	}
	if _, ok := tracker.LastSuccess(); ok || !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("late success updated database readiness")
	}
}

func TestDatabaseProbeTrackerRunRetriesAndStopsWithContext(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	tracker, err := health.NewDatabaseProbeTracker(probePingerFunc(func(context.Context) error {
		if calls.Add(1) < 2 {
			return errors.New("not yet")
		}
		return nil
	}), 20*time.Millisecond, 10*time.Millisecond, 100*time.Millisecond, &probeClock{now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- tracker.Run(ctx) }()
	deadline := time.After(time.Second)
	for tracker.Check(t.Context()) != nil {
		select {
		case <-deadline:
			t.Fatal("background database probe did not recover")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("database probe worker did not stop")
	}
	if err := tracker.Run(t.Context()); err == nil {
		t.Fatal("second database probe Run was accepted")
	}
}

func TestDatabaseProbeTrackerValidatesConstructionAndClockRegression(t *testing.T) {
	t.Parallel()
	clock := &probeClock{now: time.Now()}
	var typedNilPinger *probePinger
	var typedNilClock *probeClock
	for _, test := range []struct {
		name     string
		pinger   health.DatabasePinger
		interval time.Duration
		timeout  time.Duration
		grace    time.Duration
		clock    health.ProbeClock
	}{
		{name: "nil pinger", interval: time.Second, timeout: time.Millisecond, grace: time.Second, clock: clock},
		{name: "typed nil pinger", pinger: typedNilPinger, interval: time.Second, timeout: time.Millisecond, grace: time.Second, clock: clock},
		{name: "nil clock", pinger: &probePinger{}, interval: time.Second, timeout: time.Millisecond, grace: time.Second},
		{name: "typed nil clock", pinger: &probePinger{}, interval: time.Second, timeout: time.Millisecond, grace: time.Second, clock: typedNilClock},
		{name: "timeout exceeds interval", pinger: &probePinger{}, interval: time.Second, timeout: 2 * time.Second, grace: 2 * time.Second, clock: clock},
		{name: "timeout equals interval", pinger: &probePinger{}, interval: time.Second, timeout: time.Second, grace: 2 * time.Second, clock: clock},
		{name: "grace shorter than interval", pinger: &probePinger{}, interval: time.Second, timeout: time.Millisecond, grace: time.Millisecond, clock: clock},
	} {
		if _, err := health.NewDatabaseProbeTracker(test.pinger, test.interval, test.timeout, test.grace, test.clock); err == nil {
			t.Fatalf("invalid construction %q accepted", test.name)
		}
	}
	tracker, err := health.NewDatabaseProbeTracker(&probePinger{}, time.Second, 100*time.Millisecond, time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ProbeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(-time.Second)
	if !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("clock regression was treated as fresh")
	}
	clock.Advance(1500 * time.Millisecond)
	if !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("clock catch-up without a successful probe cleared the rollback latch")
	}
	if err := tracker.ProbeOnce(t.Context()); err != nil || tracker.Check(t.Context()) != nil {
		t.Fatalf("legal successful probe did not clear clock rollback latch: %v", err)
	}
}

func TestDatabaseProbeTrackerLatchesRollbackObservedOnlyByFailedProbe(t *testing.T) {
	t.Parallel()
	clock := &probeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}
	pinger := &probePinger{}
	tracker, err := health.NewDatabaseProbeTracker(pinger, time.Second, 100*time.Millisecond, 3*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ProbeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(-time.Second)
	pinger.SetError(errors.New("mysql unavailable"))
	if err := tracker.ProbeOnce(t.Context()); err == nil {
		t.Fatal("failed probe unexpectedly succeeded")
	}
	clock.Advance(1500 * time.Millisecond)
	if !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("failed probe did not latch an otherwise unobserved clock rollback")
	}
	pinger.SetError(nil)
	if err := tracker.ProbeOnce(t.Context()); err != nil || tracker.Check(t.Context()) != nil {
		t.Fatalf("legal success did not clear failed-probe rollback latch: %v", err)
	}
}

func TestDatabaseProbeTrackerCanceledContextDoesNotStartPing(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	newTracker := func(t *testing.T) *health.DatabaseProbeTracker {
		t.Helper()
		tracker, err := health.NewDatabaseProbeTracker(probePingerFunc(func(context.Context) error {
			calls.Add(1)
			return nil
		}), time.Second, time.Millisecond, time.Second, &probeClock{now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return tracker
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := newTracker(t).ProbeOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ProbeOnce = %v", err)
	}
	if err := newTracker(t).Run(ctx); err != nil {
		t.Fatalf("canceled Run = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled context started %d database calls", calls.Load())
	}
}

func TestDatabaseProbeTrackerBoundsNonCooperativeProbeAndAllowsShutdown(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	retryError := errors.New("retry observed")
	tracker, err := health.NewDatabaseProbeTracker(probePingerFunc(func(context.Context) error {
		if calls.Add(1) > 1 {
			return retryError
		}
		close(started)
		<-release
		return nil
	}), 20*time.Millisecond, 5*time.Millisecond, 40*time.Millisecond, &probeClock{now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	probeStarted := time.Now()
	if err := tracker.ProbeOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) || time.Since(probeStarted) > 100*time.Millisecond {
		t.Fatalf("non-cooperative ProbeOnce = %v after %s", err, time.Since(probeStarted))
	}
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- tracker.Run(ctx) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("non-cooperative probe blocked tracker shutdown")
	}
	if calls.Load() != 1 {
		t.Fatalf("in-flight gate allowed %d overlapping database probes", calls.Load())
	}
	close(release)
	retryDeadline := time.After(time.Second)
	for {
		err := tracker.ProbeOnce(t.Context())
		if errors.Is(err, retryError) {
			break
		}
		select {
		case <-retryDeadline:
			t.Fatalf("released in-flight probe was not deterministically reaped: %v", err)
		default:
		}
	}
	if _, ok := tracker.LastSuccess(); ok || !errors.Is(tracker.Check(t.Context()), health.ErrDatabaseProbeStale) {
		t.Fatal("late nil result updated the successful probe watermark")
	}
}

type probeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *probeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *probeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

type probePinger struct {
	mu  sync.Mutex
	err error
}

func (pinger *probePinger) Ping(context.Context) error {
	pinger.mu.Lock()
	defer pinger.mu.Unlock()
	return pinger.err
}

func (pinger *probePinger) SetError(err error) {
	pinger.mu.Lock()
	pinger.err = err
	pinger.mu.Unlock()
}

type probePingerFunc func(context.Context) error

func (function probePingerFunc) Ping(ctx context.Context) error { return function(ctx) }
