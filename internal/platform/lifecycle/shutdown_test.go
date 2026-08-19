package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/health"
	"github.com/asherzj/financial_configuration_center/internal/platform/lifecycle"
)

func TestShutdownRunsOrderedBoundedPhasesAndClearsReadinessFirst(t *testing.T) {
	t.Parallel()
	ready := health.NewReadiness(true)
	var mutex sync.Mutex
	var order []string
	record := func(name string) lifecycle.ShutdownFunc {
		return func(context.Context) error {
			mutex.Lock()
			defer mutex.Unlock()
			if ready.IsReady() {
				t.Errorf("%s ran while still ready", name)
			}
			order = append(order, name)
			return nil
		}
	}
	plan := lifecycle.ShutdownPlan{
		Readiness:      ready,
		Timeout:        time.Second,
		StopAccepting:  []lifecycle.ShutdownFunc{record("stop")},
		CancelWorkers:  func() { order = append(order, "cancel") },
		Drain:          []lifecycle.ShutdownFunc{record("drain")},
		FlushTelemetry: []lifecycle.ShutdownFunc{record("flush")},
		Close:          []lifecycle.ShutdownFunc{record("close")},
	}
	if err := plan.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if expected := []string{"stop", "cancel", "drain", "flush", "close"}; !reflect.DeepEqual(order, expected) {
		t.Fatalf("shutdown order=%v expected=%v", order, expected)
	}
}

func TestShutdownHonorsGlobalTimeout(t *testing.T) {
	t.Parallel()
	plan := lifecycle.ShutdownPlan{
		Readiness: health.NewReadiness(true),
		Timeout:   10 * time.Millisecond,
		Drain: []lifecycle.ShutdownFunc{func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	}
	err := plan.Run(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
}
