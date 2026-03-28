package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/store"
	"github.com/redis/go-redis/v9"
)

// mockRedis implements store.CacheStore for testing rate limiting.
type mockRedis struct {
	allowed   bool
	remaining int
	err       error
	counters  map[string]int64
}

func (m *mockRedis) SetRobotState(_ context.Context, _ *store.RobotHotState) error { return nil }
func (m *mockRedis) GetRobotState(_ context.Context, _ string) (*store.RobotHotState, error) {
	return nil, errors.New("not found")
}
func (m *mockRedis) CheckRateLimit(_ context.Context, _ string, limit int, _ time.Duration) (bool, int, time.Time, error) {
	if m.err != nil {
		return false, 0, time.Time{}, m.err
	}
	return m.allowed, m.remaining, time.Now().Add(time.Second), nil
}
func (m *mockRedis) IncrementUsageCounter(_ context.Context, tenant, metric string) (int64, error) {
	if m.counters == nil {
		m.counters = make(map[string]int64)
	}
	key := tenant + ":" + metric
	m.counters[key]++
	return m.counters[key], nil
}
func (m *mockRedis) GetUsageCounter(_ context.Context, _, _, _ string) (int64, error) { return 0, nil }
func (m *mockRedis) PublishEvent(_ context.Context, _ string, _ []byte) error        { return nil }
func (m *mockRedis) Subscribe(_ context.Context, _ ...string) *redis.PubSub          { return nil }
func (m *mockRedis) SetCacheJSON(_ context.Context, _ string, _ []byte, _ time.Duration) error { return nil }
func (m *mockRedis) GetCacheJSON(_ context.Context, _ string) ([]byte, error)        { return nil, nil }
func (m *mockRedis) Close()                                                           {}

func newAuthenticatedRequest(tenantID string) *http.Request {
	req := httptest.NewRequest("GET", "/api/v1/robots", nil)
	ctx := context.WithValue(req.Context(), auth.TenantIDKey, tenantID)
	return req.WithContext(ctx)
}

func TestRateLimiter_Allowed(t *testing.T) {
	mock := &mockRedis{allowed: true, remaining: 99}
	handler := RateLimiter(mock, 100, 200)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, newAuthenticatedRequest("tenant-1"))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "100" {
		t.Errorf("expected X-RateLimit-Limit=100, got %s", rr.Header().Get("X-RateLimit-Limit"))
	}
}

func TestRateLimiter_Exceeded(t *testing.T) {
	mock := &mockRedis{allowed: false, remaining: 0}
	handler := RateLimiter(mock, 100, 200)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called when rate limited")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, newAuthenticatedRequest("tenant-1"))

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestRateLimiter_FailClosed(t *testing.T) {
	mock := &mockRedis{err: errors.New("redis connection refused")}
	handler := RateLimiter(mock, 100, 200)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called when Redis is down")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, newAuthenticatedRequest("tenant-1"))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (fail-closed), got %d", rr.Code)
	}
}

func TestRateLimiter_NoTenant_PassesThrough(t *testing.T) {
	mock := &mockRedis{err: errors.New("should not be called")}
	called := false
	handler := RateLimiter(mock, 100, 200)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/robots", nil) // no tenant in context
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("expected handler to be called for unauthenticated request")
	}
}

func TestUsageMetering_IncrementsCounter(t *testing.T) {
	mock := &mockRedis{counters: make(map[string]int64)}
	handler := UsageMetering(mock)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, newAuthenticatedRequest("tenant-1"))
	handler.ServeHTTP(rr, newAuthenticatedRequest("tenant-1"))

	if mock.counters["tenant-1:api_calls"] != 2 {
		t.Errorf("expected 2 api_calls, got %d", mock.counters["tenant-1:api_calls"])
	}
}
