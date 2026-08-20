package adminbff

import (
	"context"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestRequestTraceContextPrefersActiveBFFSpan(t *testing.T) {
	t.Parallel()
	active := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request = request.WithContext(trace.ContextWithSpanContext(context.Background(), active))
	traceID, traceParent, _ := requestTraceContext(request)
	if traceID != active.TraceID().String() || traceParent != "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01" {
		t.Fatalf("traceID=%q traceparent=%q", traceID, traceParent)
	}
}

func TestRequestTraceContextPropagatesActiveRootWithoutIncomingHeader(t *testing.T) {
	t.Parallel()
	active := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		SpanID:  trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1},
	})
	request := httptest.NewRequest("GET", "/", nil).WithContext(trace.ContextWithSpanContext(context.Background(), active))
	traceID, traceParent, _ := requestTraceContext(request)
	if traceID != active.TraceID().String() || traceParent != "00-100f0e0d0c0b0a090807060504030201-0807060504030201-00" {
		t.Fatalf("traceID=%q traceparent=%q", traceID, traceParent)
	}
}
