package health

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

var ErrDatabaseProbeStale = errors.New("database has no recent successful probe")

type DatabasePinger interface {
	Ping(context.Context) error
}

type ProbeClock interface {
	Now() time.Time
}

type DatabaseProbeTracker struct {
	pinger   DatabasePinger
	interval time.Duration
	timeout  time.Duration
	grace    time.Duration
	clock    ProbeClock

	mu                   sync.RWMutex
	lastSuccess          time.Time
	lastClockObservation time.Time
	clockInvalid         bool
	runStarted           atomic.Bool

	probeMu  sync.Mutex
	inFlight *databaseProbeFlight
}

type databaseProbeFlight struct {
	done chan struct{}
	err  error
}

func NewDatabaseProbeTracker(pinger DatabasePinger, interval, timeout, grace time.Duration, clock ProbeClock) (*DatabaseProbeTracker, error) {
	if isNilProbeDependency(pinger) || isNilProbeDependency(clock) {
		return nil, errors.New("database probe pinger and clock are required")
	}
	if interval <= 0 || timeout <= 0 || timeout >= interval {
		return nil, errors.New("database probe timeout must be positive and shorter than its interval")
	}
	if grace < interval {
		return nil, errors.New("database probe grace must be at least one interval")
	}
	return &DatabaseProbeTracker{pinger: pinger, interval: interval, timeout: timeout, grace: grace, clock: clock}, nil
}

func (tracker *DatabaseProbeTracker) ProbeOnce(ctx context.Context) error {
	if tracker == nil || isNilProbeDependency(tracker.pinger) || isNilProbeDependency(tracker.clock) || ctx == nil {
		return errors.New("database probe tracker is not constructed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	flight := tracker.startOrJoinProbe(ctx)
	waitContext, cancel := context.WithTimeout(ctx, tracker.timeout)
	defer cancel()
	select {
	case <-flight.done:
		if flight.err != nil {
			tracker.observeFailedProbe()
		}
		return flight.err
	case <-waitContext.Done():
		tracker.observeFailedProbe()
		return waitContext.Err()
	}
}

func (tracker *DatabaseProbeTracker) startOrJoinProbe(parent context.Context) *databaseProbeFlight {
	tracker.probeMu.Lock()
	defer tracker.probeMu.Unlock()
	if tracker.inFlight != nil {
		return tracker.inFlight
	}
	probeContext, cancel := context.WithTimeout(parent, tracker.timeout)
	deadline, _ := probeContext.Deadline()
	flight := &databaseProbeFlight{done: make(chan struct{})}
	tracker.inFlight = flight
	go func() {
		err := tracker.pinger.Ping(probeContext)
		if err == nil {
			if contextErr := probeContext.Err(); contextErr != nil {
				err = contextErr
			} else if time.Now().After(deadline) {
				err = context.DeadlineExceeded
			} else {
				err = tracker.recordSuccess()
			}
		}
		cancel()
		flight.err = err
		tracker.probeMu.Lock()
		if tracker.inFlight == flight {
			tracker.inFlight = nil
		}
		tracker.probeMu.Unlock()
		close(flight.done)
	}()
	return flight
}

func (tracker *DatabaseProbeTracker) recordSuccess() error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	observed := tracker.clock.Now()
	if !tracker.observeClockLocked(observed) {
		return errors.New("database probe clock regressed")
	}
	tracker.lastSuccess = observed
	tracker.clockInvalid = false
	return nil
}

func (tracker *DatabaseProbeTracker) observeFailedProbe() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.observeClockLocked(tracker.clock.Now())
}

func (tracker *DatabaseProbeTracker) observeClockLocked(observed time.Time) bool {
	if observed.IsZero() || !tracker.lastClockObservation.IsZero() && observed.Before(tracker.lastClockObservation) {
		tracker.clockInvalid = true
		return false
	}
	if tracker.lastClockObservation.IsZero() || observed.After(tracker.lastClockObservation) {
		tracker.lastClockObservation = observed
	}
	return true
}

func (tracker *DatabaseProbeTracker) Run(ctx context.Context) error {
	if tracker == nil || isNilProbeDependency(tracker.pinger) || isNilProbeDependency(tracker.clock) || ctx == nil {
		return errors.New("database probe tracker is not constructed")
	}
	if !tracker.runStarted.CompareAndSwap(false, true) {
		return errors.New("database probe tracker Run may only be called once")
	}
	_ = tracker.ProbeOnce(ctx)
	ticker := time.NewTicker(tracker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = tracker.ProbeOnce(ctx)
		}
	}
}

func (tracker *DatabaseProbeTracker) Check(ctx context.Context) error {
	if tracker == nil || isNilProbeDependency(tracker.clock) || ctx == nil {
		return ErrDatabaseProbeStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	now := tracker.clock.Now()
	if !tracker.observeClockLocked(now) || tracker.clockInvalid || tracker.lastSuccess.IsZero() {
		return ErrDatabaseProbeStale
	}
	if age := now.Sub(tracker.lastSuccess); age > tracker.grace {
		return ErrDatabaseProbeStale
	}
	return nil
}

func isNilProbeDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (tracker *DatabaseProbeTracker) LastSuccess() (time.Time, bool) {
	if tracker == nil {
		return time.Time{}, false
	}
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.lastSuccess, !tracker.lastSuccess.IsZero()
}
