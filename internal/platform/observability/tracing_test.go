package observability_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/asherzj/financial_configuration_center/internal/platform/observability"
)

func TestTracingUsesInjectedExporterAndW3CPropagation(t *testing.T) {
	t.Parallel()
	exporter := tracetest.NewInMemoryExporter()
	tracing, err := observability.NewTracing(context.Background(), observability.TraceConfig{
		ServiceName: "control-plane",
		Version:     "v1",
		Environment: "test",
		InstanceID:  "instance-a",
		SampleRatio: 1,
	}, exporter)
	if err != nil {
		t.Fatal(err)
	}
	_, span := tracing.Provider.Tracer("test").Start(context.Background(), "catalog.command")
	span.End()
	if err := tracing.Provider.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "catalog.command" {
		t.Fatalf("exported spans=%+v", spans)
	}
	attributes := map[attribute.Key]string{}
	for _, item := range spans[0].Resource.Attributes() {
		attributes[item.Key] = item.Value.AsString()
	}
	if attributes["service.name"] != "control-plane" || attributes["deployment.environment.name"] != "test" {
		t.Fatalf("resource attributes=%+v", attributes)
	}
	carrier := propagation.MapCarrier{}
	ctx, parent := tracing.Provider.Tracer("test").Start(context.Background(), "parent")
	tracing.Propagator.Inject(ctx, carrier)
	parent.End()
	if carrier.Get("traceparent") == "" {
		t.Fatalf("W3C traceparent not injected: %+v", carrier)
	}
	if err := tracing.Provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTraceConfigRejectsInvalidSampling(t *testing.T) {
	t.Parallel()
	if _, err := observability.NewTracing(context.Background(), observability.TraceConfig{ServiceName: "control-plane", SampleRatio: 1.1}, nil); err == nil {
		t.Fatal("invalid trace sample ratio accepted")
	}
}
