package auth

import (
	"net/http"
	"strings"

	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

// Authenticate requires a valid bearer token and puts the principal in the request context.
func Authenticate(verifier Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agent-platform"`)
				respond.Error(w, r, http.StatusUnauthorized, "unauthorized", "a bearer token is required")
				return
			}
			principal, err := verifier.Verify(r.Context(), strings.TrimSpace(token))
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agent-platform", error="invalid_token"`)
				respond.Error(w, r, http.StatusUnauthorized, "unauthorized", "the bearer token is invalid or expired")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}
