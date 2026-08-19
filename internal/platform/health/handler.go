package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"sync/atomic"
	"time"
)

type Readiness struct {
	ready atomic.Bool
}

func NewReadiness(initial bool) *Readiness {
	readiness := &Readiness{}
	readiness.ready.Store(initial)
	return readiness
}

func (readiness *Readiness) Set(ready bool) {
	if readiness != nil {
		readiness.ready.Store(ready)
	}
}

func (readiness *Readiness) IsReady() bool {
	return readiness != nil && readiness.ready.Load()
}

type Check struct {
	Name string
	Run  func(context.Context) error
}

type response struct {
	Status string   `json:"status"`
	Failed []string `json:"failed,omitempty"`
}

var componentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func NewHandler(readiness *Readiness, timeout time.Duration, metrics http.Handler, checks ...Check) (http.Handler, error) {
	if readiness == nil {
		return nil, errors.New("readiness gate is required")
	}
	if timeout <= 0 {
		return nil, errors.New("readiness timeout must be positive")
	}
	if metrics == nil {
		return nil, errors.New("metrics handler is required")
	}
	seen := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if !componentNamePattern.MatchString(check.Name) || check.Run == nil {
			return nil, errors.New("readiness check name and function are required")
		}
		if _, exists := seen[check.Name]; exists {
			return nil, errors.New("readiness check names must be unique")
		}
		seen[check.Name] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.Handle("/healthz", safeMethod(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, response{Status: "ok"})
	})))
	mux.Handle("/readyz", safeMethod(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !readiness.IsReady() {
			writeJSON(writer, http.StatusServiceUnavailable, response{Status: "not_ready", Failed: []string{"accepting_requests"}})
			return
		}
		failed := runChecks(request.Context(), timeout, checks)
		if len(failed) > 0 {
			writeJSON(writer, http.StatusServiceUnavailable, response{Status: "not_ready", Failed: failed})
			return
		}
		writeJSON(writer, http.StatusOK, response{Status: "ready"})
	})))
	mux.Handle("/metrics", safeMethod(metrics))
	return mux, nil
}

func runChecks(parent context.Context, timeout time.Duration, checks []Check) []string {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(checks))
	for _, check := range checks {
		check := check
		go func() {
			results <- result{name: check.Name, err: check.Run(ctx)}
		}()
	}
	failed := make([]string, 0)
	completed := make(map[string]struct{}, len(checks))
	for range checks {
		select {
		case checked := <-results:
			completed[checked.name] = struct{}{}
			if checked.err != nil {
				failed = append(failed, checked.name)
			}
		case <-ctx.Done():
			for _, check := range checks {
				if _, ok := completed[check.Name]; !ok {
					failed = append(failed, check.Name)
				}
			}
			sort.Strings(failed)
			return failed
		}
	}
	sort.Strings(failed)
	return failed
}

func safeMethod(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value response) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
