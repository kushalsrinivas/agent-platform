package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
)

func TestRateLimitPerPrincipal(t *testing.T) {
	limited := RateLimit(60, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/orgs", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{ID: "user_1"}))

	first := httptest.NewRecorder()
	limited.ServeHTTP(first, request)
	if first.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	limited.ServeHTTP(second, request)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("429 response must include Retry-After")
	}

	otherPrincipal := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/orgs", nil)
	otherPrincipal = otherPrincipal.WithContext(auth.WithPrincipal(otherPrincipal.Context(), auth.Principal{ID: "user_2"}))
	other := httptest.NewRecorder()
	limited.ServeHTTP(other, otherPrincipal)
	if other.Code != http.StatusOK {
		t.Fatalf("another principal was limited: %d", other.Code)
	}
}
