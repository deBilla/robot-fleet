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

// QuotaEnforcement checks if the tenant has exceeded their monthly API call or inference quota.
// tierResolver maps tenantID → tier name (e.g., "free", "pro", "enterprise").
func QuotaEnforcement(cache store.CacheStore, tierResolver func(string) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := auth.GetTenantID(r.Context())
			if tenantID == "" {
				next.ServeHTTP(w, r)
				return
			}

			tierName := "free"
			if tierResolver != nil {
				tierName = tierResolver(tenantID)
			}

			// Enterprise tier has no limits
			if tierName == "enterprise" {
				next.ServeHTTP(w, r)
				return
			}

			date := time.Now().Format("2006-01-02")
			apiCalls, _ := cache.GetUsageCounter(r.Context(), tenantID, "api_calls", date)

			// Daily limit = monthly limit (approximation: monthly / 30)
			var dailyLimit int64
			switch tierName {
			case "free":
				dailyLimit = 1000 / 30 // ~33 calls/day
			case "pro":
				dailyLimit = 100_000 / 30 // ~3333 calls/day
			default:
				dailyLimit = 1000 / 30
			}

			if dailyLimit > 0 && apiCalls > dailyLimit {
				w.Header().Set("X-Quota-Exceeded", "true")
				w.Header().Set("X-Quota-Limit", strconv.FormatInt(dailyLimit, 10))
				http.Error(w, `{"error":"daily API quota exceeded, upgrade your plan"}`, http.StatusPaymentRequired)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
