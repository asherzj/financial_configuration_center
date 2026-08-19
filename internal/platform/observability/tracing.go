package observability

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type TraceConfig struct {
	ServiceName string
	Version     string
	Environment string
	InstanceID  string
	SampleRatio float64
}

type Tracing struct {
	Provider   *sdktrace.TracerProvider
	Propagator propagation.TextMapPropagator
}

func NewTracing(_ context.Context, config TraceConfig, exporter sdktrace.SpanExporter) (*Tracing, error) {
	if config.ServiceName == "" {
		return nil, errors.New("trace service name is required")
	}
	if config.SampleRatio < 0 || config.SampleRatio > 1 {
		return nil, errors.New("trace sample ratio must be between zero and one")
	}
	serviceResource := resource.NewSchemaless(
		attribute.String("service.name", config.ServiceName),
		attribute.String("service.version", config.Version),
		attribute.String("deployment.environment.name", config.Environment),
		attribute.String("service.instance.id", config.InstanceID),
	)
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(serviceResource),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
	}
	if exporter != nil {
		options = append(options, sdktrace.WithBatcher(exporter))
	}
	return &Tracing{
		Provider: sdktrace.NewTracerProvider(options...),
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}, nil
}
