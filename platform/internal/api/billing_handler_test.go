package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
	"github.com/redis/go-redis/v9"
)

// mockBillingStore implements store.BillingRepository for handler tests.
type mockBillingStore struct {
	tenant   *store.TenantRecord
	invoices []*store.InvoiceRecord
}

func (m *mockBillingStore) UpsertTenant(_ context.Context, t *store.TenantRecord) error { return nil }
func (m *mockBillingStore) GetTenant(_ context.Context, id string) (*store.TenantRecord, error) {
	if m.tenant != nil && m.tenant.ID == id {
		return m.tenant, nil
	}
	return nil, context.DeadlineExceeded
}
func (m *mockBillingStore) ListTenants(_ context.Context) ([]*store.TenantRecord, error) {
	return nil, nil
}
func (m *mockBillingStore) UpdateTenantTier(_ context.Context, id, tier string) error { return nil }
func (m *mockBillingStore) UpsertDailyUsage(_ context.Context, _ string, _ time.Time, _ string, _ int64) error {
	return nil
}
func (m *mockBillingStore) GetDailyUsage(_ context.Context, _ string, _, _ time.Time) ([]*store.UsageDailyRecord, error) {
	return nil, nil
}
func (m *mockBillingStore) CreateInvoice(_ context.Context, inv *store.InvoiceRecord) error {
	return nil
}
func (m *mockBillingStore) GetInvoice(_ context.Context, id string) (*store.InvoiceRecord, error) {
	for _, inv := range m.invoices {
		if inv.ID == id {
			return inv, nil
		}
	}
	return nil, context.DeadlineExceeded
}
func (m *mockBillingStore) ListInvoices(_ context.Context, tenantID string, limit int) ([]*store.InvoiceRecord, error) {
	return m.invoices, nil
}
func (m *mockBillingStore) UpdateInvoiceStatus(_ context.Context, _, _ string) error { return nil }
func (m *mockBillingStore) CreateTierChangeEvent(_ context.Context, _ *store.TierChangeEventRecord) error {
	return nil
}

// mockBillingCacheStore is a minimal CacheStore mock for billing handler tests.
type mockBillingCacheStore struct{}

func (m *mockBillingCacheStore) SetRobotState(_ context.Context, _ *store.RobotHotState) error {
	return nil
}
func (m *mockBillingCacheStore) GetRobotState(_ context.Context, _ string) (*store.RobotHotState, error) {
	return nil, nil
}
func (m *mockBillingCacheStore) CheckRateLimit(_ context.Context, _ string, _ int, _ time.Duration) (bool, int, time.Time, error) {
	return true, 0, time.Time{}, nil
}
func (m *mockBillingCacheStore) IncrementUsageCounter(_ context.Context, _, _ string) (int64, error) {
	return 0, nil
}
func (m *mockBillingCacheStore) GetUsageCounter(_ context.Context, _, _, _ string) (int64, error) {
	return 0, nil
}
func (m *mockBillingCacheStore) PublishEvent(_ context.Context, _ string, _ []byte) error { return nil }
func (m *mockBillingCacheStore) Subscribe(_ context.Context, _ ...string) *redis.PubSub  { return nil }
func (m *mockBillingCacheStore) SetCacheJSON(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (m *mockBillingCacheStore) GetCacheJSON(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (m *mockBillingCacheStore) CheckCommandDedup(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (m *mockBillingCacheStore) SetCommandDedup(_ context.Context, _ string, _ int64) error {
	return nil
}
func (m *mockBillingCacheStore) Close() {}

func withTenantContext(r *http.Request, tenantID string) *http.Request {
	ctx := context.WithValue(r.Context(), auth.TenantIDKey, tenantID)
	return r.WithContext(ctx)
}

func TestBillingHandler_GetSubscription(t *testing.T) {
	billingStore := &mockBillingStore{
		tenant: &store.TenantRecord{ID: "tenant-dev", Tier: "pro", Name: "Dev Tenant"},
	}
	svc := service.NewTemporalBillingService(billingStore, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("GET", "/api/v1/billing/subscription", nil)
	req = withTenantContext(req, "tenant-dev")
	w := httptest.NewRecorder()

	handler.GetSubscription(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp store.TenantRecord
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Tier != "pro" {
		t.Errorf("expected tier pro, got %s", resp.Tier)
	}
}

func TestBillingHandler_GetSubscription_NotFound(t *testing.T) {
	billingStore := &mockBillingStore{}
	svc := service.NewTemporalBillingService(billingStore, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("GET", "/api/v1/billing/subscription", nil)
	req = withTenantContext(req, "nonexistent")
	w := httptest.NewRecorder()

	handler.GetSubscription(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestBillingHandler_ListInvoices(t *testing.T) {
	billingStore := &mockBillingStore{
		invoices: []*store.InvoiceRecord{
			{ID: "inv-1", TenantID: "tenant-dev", Total: 99.0, Status: "paid", LineItems: map[string]any{}},
			{ID: "inv-2", TenantID: "tenant-dev", Total: 99.5, Status: "draft", LineItems: map[string]any{}},
		},
	}
	svc := service.NewTemporalBillingService(billingStore, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("GET", "/api/v1/billing/invoices", nil)
	req = withTenantContext(req, "tenant-dev")
	w := httptest.NewRecorder()

	handler.ListInvoices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var invoices []*store.InvoiceRecord
	json.NewDecoder(w.Body).Decode(&invoices)
	if len(invoices) != 2 {
		t.Errorf("expected 2 invoices, got %d", len(invoices))
	}
}

func TestBillingHandler_GetInvoice(t *testing.T) {
	billingStore := &mockBillingStore{
		invoices: []*store.InvoiceRecord{
			{ID: "inv-1", TenantID: "tenant-dev", Total: 99.0, Status: "paid", LineItems: map[string]any{}},
		},
	}
	svc := service.NewTemporalBillingService(billingStore, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("GET", "/api/v1/billing/invoices/inv-1", nil)
	req.SetPathValue("id", "inv-1")
	req = withTenantContext(req, "tenant-dev")
	w := httptest.NewRecorder()

	handler.GetInvoice(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestBillingHandler_GetInvoice_NotFound(t *testing.T) {
	billingStore := &mockBillingStore{}
	svc := service.NewTemporalBillingService(billingStore, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("GET", "/api/v1/billing/invoices/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	req = withTenantContext(req, "tenant-dev")
	w := httptest.NewRecorder()

	handler.GetInvoice(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestBillingHandler_ChangeTier_MissingTier(t *testing.T) {
	svc := service.NewTemporalBillingService(&mockBillingStore{}, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("PUT", "/api/v1/billing/subscription/tier", strings.NewReader(`{}`))
	req = withTenantContext(req, "tenant-dev")
	w := httptest.NewRecorder()

	handler.ChangeTier(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestBillingHandler_ChangeTier_InvalidBody(t *testing.T) {
	svc := service.NewTemporalBillingService(&mockBillingStore{}, &mockBillingCacheStore{}, nil)
	handler := NewBillingHandler(svc)

	req := httptest.NewRequest("PUT", "/api/v1/billing/subscription/tier", strings.NewReader(`not json`))
	req = withTenantContext(req, "tenant-dev")
	w := httptest.NewRecorder()

	handler.ChangeTier(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
