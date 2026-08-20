package rpcauth

import (
	"context"
	"errors"
	"strings"

	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	kitexmetadata "github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const maxRequestIDBytes = 256

// InternalPrincipalResolver projects the identity established by JWT
// middleware and validated request metadata into Control Plane handlers.
type InternalPrincipalResolver struct{}

func (InternalPrincipalResolver) Subject(ctx context.Context) (string, error) {
	identity, ok := InternalCallerIdentityFromContext(ctx)
	if !ok || strings.TrimSpace(identity.Subject) == "" {
		return "", errors.New("internal caller identity is required")
	}
	return identity.Subject, nil
}

func (InternalPrincipalResolver) Roles(ctx context.Context) ([]string, error) {
	identity, ok := InternalCallerIdentityFromContext(ctx)
	if !ok {
		return nil, errors.New("internal caller identity is required")
	}
	return append([]string(nil), identity.Roles...), nil
}

func (InternalPrincipalResolver) Scopes(ctx context.Context) ([]platformauth.ScopePattern, error) {
	identity, ok := InternalCallerIdentityFromContext(ctx)
	if !ok {
		return nil, errors.New("internal caller identity is required")
	}
	return append([]platformauth.ScopePattern(nil), identity.Scopes...), nil
}

func (InternalPrincipalResolver) RequestID(ctx context.Context) string {
	metadata, ok := kitexmetadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := metadata.Get("x-request-id")
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxRequestIDBytes || values[0] != strings.TrimSpace(values[0]) {
		return ""
	}
	return values[0]
}

func (InternalPrincipalResolver) TraceID(ctx context.Context) string {
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		return spanContext.TraceID().String()
	}
	metadata, ok := kitexmetadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	carrier := propagation.MapCarrier{}
	for _, key := range []string{"traceparent", "tracestate"} {
		values := metadata.Get(key)
		if len(values) == 1 {
			carrier.Set(key, values[0])
		}
	}
	extracted := propagation.TraceContext{}.Extract(ctx, carrier)
	spanContext := trace.SpanContextFromContext(extracted)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func (InternalPrincipalResolver) DisplayName(ctx context.Context) (string, error) {
	identity, ok := InternalCallerIdentityFromContext(ctx)
	if !ok {
		return "", errors.New("internal caller identity is required")
	}
	return identity.DisplayName, nil
}
