package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// RateLimiter middleware using sliding window rate limiting via CacheStore.
func RateLimiter(cache store.CacheStore, rps, burst int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := auth.GetTenantID(r.Context())
			if tenantID == "" {
				// Unauthenticated requests pass through (auth middleware handles rejection)
				next.ServeHTTP(w, r)
				return
			}

			key := fmt.Sprintf("ratelimit:%s", tenantID)
			allowed, remaining, resetTime, err := cache.CheckRateLimit(r.Context(), key, rps, time.Second)
			if err != nil {
				// Fail-closed: reject requests when rate limiter is unavailable
				http.Error(w, `{"error":"rate limiter unavailable"}`, http.StatusServiceUnavailable)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rps))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

			if !allowed {
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// UsageMetering records API usage for billing via CacheStore.
func UsageMetering(cache store.CacheStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := auth.GetTenantID(r.Context())
			if tenantID != "" {
				cache.IncrementUsageCounter(r.Context(), tenantID, "api_calls")
			}
			next.ServeHTTP(w, r)
		})
	}
}
