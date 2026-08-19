package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/health"
)

func TestHealthAndReadinessDoNotLeakCheckErrors(t *testing.T) {
	t.Parallel()
	ready := health.NewReadiness(false)
	metrics := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) })
	handler, err := health.NewHandler(ready, 20*time.Millisecond, metrics,
		health.Check{Name: "database", Run: func(context.Context) error { return errors.New("mysql://root:secret@database") }},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertStatus(t, handler, "/healthz", http.StatusOK)
	assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	ready.Set(true)
	recorder := assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	if !strings.Contains(recorder.Body.String(), "database") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("unsafe readiness response: %s", recorder.Body.String())
	}
	assertStatus(t, handler, "/metrics", http.StatusOK)
}

func TestReadinessChecksAreBoundedByTimeout(t *testing.T) {
	t.Parallel()
	ready := health.NewReadiness(true)
	handler, err := health.NewHandler(ready, 10*time.Millisecond, http.NotFoundHandler(), health.Check{
		Name: "snapshot",
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("readiness timeout took %s", elapsed)
	}
}

func TestOperationsEndpointsRejectUnsafeMethods(t *testing.T) {
	t.Parallel()
	handler, err := health.NewHandler(health.NewReadiness(true), time.Second, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func assertStatus(t *testing.T, handler http.Handler, path string, expected int) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != expected {
		t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
	}
	return recorder
}
