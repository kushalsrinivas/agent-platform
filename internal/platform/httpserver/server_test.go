package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestHealthAndReadiness(t *testing.T) {
	ready := false
	order := []string{}

	srv := New(Options{
		Addr:   ":0",
		Logger: zap.NewNop(),
		Middlewares: []func(http.Handler) http.Handler{
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					order = append(order, "mw")
					next.ServeHTTP(w, r)
				})
			},
		},
		Checks: []Check{func(context.Context) error {
			if ready {
				return nil
			}
			return errors.New("dependency down")
		}},
	})

	get := func(path string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		srv.Router().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", code)
	}
	if len(order) != 1 || order[0] != "mw" {
		t.Fatalf("middleware was not applied before routes: %v", order)
	}

	if code := get("/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503", code)
	}

	ready = true
	if code := get("/readyz"); code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", code)
	}
}
