package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/health"
)

type ShutdownFunc func(context.Context) error

type ShutdownPlan struct {
	Readiness      *health.Readiness
	Timeout        time.Duration
	StopAccepting  []ShutdownFunc
	CancelWorkers  context.CancelFunc
	Drain          []ShutdownFunc
	FlushTelemetry []ShutdownFunc
	Close          []ShutdownFunc
}

func (plan ShutdownPlan) Run(parent context.Context) error {
	if plan.Readiness == nil {
		return errors.New("shutdown readiness gate is required")
	}
	if plan.Timeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	plan.Readiness.Set(false)
	ctx, cancel := context.WithTimeout(parent, plan.Timeout)
	defer cancel()

	var failures []error
	if err := runPhase(ctx, plan.StopAccepting); err != nil {
		failures = append(failures, err)
	}
	if plan.CancelWorkers != nil {
		plan.CancelWorkers()
	}
	for _, phase := range [][]ShutdownFunc{plan.Drain, plan.FlushTelemetry, plan.Close} {
		if err := runPhase(ctx, phase); err != nil {
			failures = append(failures, err)
		}
	}
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func runPhase(ctx context.Context, functions []ShutdownFunc) error {
	var failures []error
	for _, function := range functions {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if function == nil {
			continue
		}
		if err := function(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
