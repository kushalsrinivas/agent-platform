// Package middleware contains HTTP concerns shared by API modules.
package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/agent-platform/agent-platform/internal/platform/metrics"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

// BodyLimit caps request bodies for mutating methods. Read-only requests have no body.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

type rateBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimit applies an in-memory token bucket per principal. Rate limits are operational, not correctness: a
// restart resets them, and multiple API replicas each enforce their own bucket. Idle buckets are evicted so a
// long-lived process does not accumulate one entry per principal forever.
func RateLimit(perMinute, burst int) func(http.Handler) http.Handler {
	if perMinute <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	limit := rate.Limit(float64(perMinute) / 60.0)
	if burst <= 0 {
		burst = perMinute
	}
	var mu sync.Mutex
	buckets := make(map[string]*rateBucket)
	lastSweep := time.Now()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.PrincipalFrom(r.Context())
			key := r.RemoteAddr
			if ok {
				key = principal.ID
			}

			now := time.Now()
			mu.Lock()
			if now.Sub(lastSweep) > 5*time.Minute {
				for bucketKey, bucket := range buckets {
					if now.Sub(bucket.lastSeen) > 10*time.Minute {
						delete(buckets, bucketKey)
					}
				}
				lastSweep = now
			}
			bucket, exists := buckets[key]
			if !exists {
				bucket = &rateBucket{limiter: rate.NewLimiter(limit, burst)}
				buckets[key] = bucket
			}
			bucket.lastSeen = now
			allowed := bucket.limiter.Allow()
			mu.Unlock()

			if !allowed {
				metrics.RateLimited.WithLabelValues("api").Inc()
				w.Header().Set("Retry-After", strconv.Itoa(1))
				respond.Error(w, r, http.StatusTooManyRequests, "rate_limited", "request rate exceeded; retry shortly")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
