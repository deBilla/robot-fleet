package activities

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
	"github.com/redis/go-redis/v9"
)

// mockBillingRepo is a minimal mock for BillingRepository used in activity tests.
type mockBillingRepo struct {
	tenants    map[string]*store.TenantRecord
	dailyUsage []*store.UsageDailyRecord
	invoices   map[string]*store.InvoiceRecord
	tierEvents []*store.TierChangeEventRecord

	upsertDailyCalls int
	createInvCalls   int
}

func newMockBillingRepo() *mockBillingRepo {
	return &mockBillingRepo{
		tenants:  make(map[string]*store.TenantRecord),
		invoices: make(map[string]*store.InvoiceRecord),
	}
}

func (m *mockBillingRepo) UpsertTenant(_ context.Context, t *store.TenantRecord) error {
	m.tenants[t.ID] = t
	return nil
}

func (m *mockBillingRepo) GetTenant(_ context.Context, id string) (*store.TenantRecord, error) {
	t, ok := m.tenants[id]
	if !ok {
		return nil, fmt.Errorf("tenant %s not found", id)
	}
	return t, nil
}

func (m *mockBillingRepo) ListTenants(_ context.Context) ([]*store.TenantRecord, error) {
	var out []*store.TenantRecord
	for _, t := range m.tenants {
		out = append(out, t)
	}
	return out, nil
}

func (m *mockBillingRepo) UpdateTenantTier(_ context.Context, id, tier string) error {
	t, ok := m.tenants[id]
	if !ok {
		return fmt.Errorf("tenant %s not found", id)
	}
	t.Tier = tier
	return nil
}

func (m *mockBillingRepo) UpsertDailyUsage(_ context.Context, tenantID string, date time.Time, metric string, count int64) error {
	m.upsertDailyCalls++
	m.dailyUsage = append(m.dailyUsage, &store.UsageDailyRecord{
		TenantID: tenantID,
		Date:     date,
		Metric:   metric,
		Count:    count,
	})
	return nil
}

func (m *mockBillingRepo) GetDailyUsage(_ context.Context, tenantID string, start, end time.Time) ([]*store.UsageDailyRecord, error) {
	var out []*store.UsageDailyRecord
	for _, r := range m.dailyUsage {
		if r.TenantID == tenantID && !r.Date.Before(start) && r.Date.Before(end) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockBillingRepo) CreateInvoice(_ context.Context, inv *store.InvoiceRecord) error {
	m.createInvCalls++
	m.invoices[inv.ID] = inv
	return nil
}

func (m *mockBillingRepo) GetInvoice(_ context.Context, id string) (*store.InvoiceRecord, error) {
	inv, ok := m.invoices[id]
	if !ok {
		return nil, fmt.Errorf("invoice %s not found", id)
	}
	return inv, nil
}

func (m *mockBillingRepo) ListInvoices(_ context.Context, tenantID string, limit int) ([]*store.InvoiceRecord, error) {
	var out []*store.InvoiceRecord
	for _, inv := range m.invoices {
		if inv.TenantID == tenantID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (m *mockBillingRepo) UpdateInvoiceStatus(_ context.Context, id, status string) error {
	inv, ok := m.invoices[id]
	if !ok {
		return fmt.Errorf("invoice %s not found", id)
	}
	inv.Status = status
	return nil
}

func (m *mockBillingRepo) CreateTierChangeEvent(_ context.Context, e *store.TierChangeEventRecord) error {
	m.tierEvents = append(m.tierEvents, e)
	return nil
}

// mockBillingCache is a minimal mock for CacheStore used in billing activity tests.
type mockBillingCache struct {
	counters map[string]int64
}

func newMockBillingCache() *mockBillingCache {
	return &mockBillingCache{counters: make(map[string]int64)}
}

func (m *mockBillingCache) SetRobotState(_ context.Context, _ *store.RobotHotState) error {
	return nil
}
func (m *mockBillingCache) GetRobotState(_ context.Context, _ string) (*store.RobotHotState, error) {
	return nil, nil
}
func (m *mockBillingCache) CheckRateLimit(_ context.Context, _ string, _ int, _ time.Duration) (bool, int, time.Time, error) {
	return true, 0, time.Time{}, nil
}
func (m *mockBillingCache) IncrementUsageCounter(_ context.Context, _, _ string) (int64, error) {
	return 0, nil
}
func (m *mockBillingCache) GetUsageCounter(_ context.Context, tenantID, metric, date string) (int64, error) {
	key := fmt.Sprintf("%s:%s:%s", tenantID, metric, date)
	return m.counters[key], nil
}
func (m *mockBillingCache) PublishEvent(_ context.Context, _ string, _ []byte) error { return nil }
func (m *mockBillingCache) Subscribe(_ context.Context, _ ...string) *redis.PubSub  { return nil }
func (m *mockBillingCache) SetCacheJSON(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (m *mockBillingCache) GetCacheJSON(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (m *mockBillingCache) CheckCommandDedup(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (m *mockBillingCache) SetCommandDedup(_ context.Context, _ string, _ int64) error { return nil }
func (m *mockBillingCache) Close()                                                     {}

func (m *mockBillingCache) setCounter(tenantID, metric, date string, count int64) {
	key := fmt.Sprintf("%s:%s:%s", tenantID, metric, date)
	m.counters[key] = count
}

func TestAggregateUsage_PersistsRedisCounters(t *testing.T) {
	repo := newMockBillingRepo()
	cache := newMockBillingCache()
	cache.setCounter("tenant-1", "api_calls", "2026-03-15", 500)
	cache.setCounter("tenant-1", "inference_calls", "2026-03-15", 42)

	acts := &BillingActivities{Billing: repo, Cache: cache}

	result, err := acts.AggregateUsage(context.Background(), AggregateUsageInput{
		TenantID: "tenant-1",
		Date:     "2026-03-15",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Counts["api_calls"] != 500 {
		t.Errorf("expected 500 api_calls, got %d", result.Counts["api_calls"])
	}
	if result.Counts["inference_calls"] != 42 {
		t.Errorf("expected 42 inference_calls, got %d", result.Counts["inference_calls"])
	}
	if repo.upsertDailyCalls != 2 {
		t.Errorf("expected 2 upsert calls, got %d", repo.upsertDailyCalls)
	}
}

func TestAggregateUsage_ZeroOnMissingRedisKey(t *testing.T) {
	repo := newMockBillingRepo()
	cache := newMockBillingCache()
	// No counters set — should default to 0

	acts := &BillingActivities{Billing: repo, Cache: cache}

	result, err := acts.AggregateUsage(context.Background(), AggregateUsageInput{
		TenantID: "tenant-empty",
		Date:     "2026-03-15",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Counts["api_calls"] != 0 {
		t.Errorf("expected 0 api_calls, got %d", result.Counts["api_calls"])
	}
}

func TestGenerateInvoice_FreeTierWithOverage(t *testing.T) {
	repo := newMockBillingRepo()
	// Seed 30 days of usage: 50 api_calls/day = 1500 total (1000 included in free)
	for day := range 30 {
		date := time.Date(2026, 3, day+1, 0, 0, 0, 0, time.UTC)
		repo.dailyUsage = append(repo.dailyUsage, &store.UsageDailyRecord{
			TenantID: "tenant-1",
			Date:     date,
			Metric:   "api_calls",
			Count:    50,
		})
	}

	acts := &BillingActivities{Billing: repo}

	result, err := acts.GenerateInvoice(context.Background(), GenerateInvoiceInput{
		InvoiceID:   "inv-001",
		TenantID:    "tenant-1",
		PeriodStart: "2026-03-01",
		PeriodEnd:   "2026-04-01",
		Tier:        "free",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// free tier: 1000 included, 500 overage at $0.001 = $0.50
	if result.Total != 0.5 {
		t.Errorf("expected total 0.5, got %f", result.Total)
	}

	inv := repo.invoices["inv-001"]
	if inv == nil {
		t.Fatal("invoice not persisted")
	}
	if inv.Status != "draft" {
		t.Errorf("expected status draft, got %s", inv.Status)
	}
}

func TestGenerateInvoice_ProTierBaseCharge(t *testing.T) {
	repo := newMockBillingRepo()
	acts := &BillingActivities{Billing: repo}

	result, err := acts.GenerateInvoice(context.Background(), GenerateInvoiceInput{
		InvoiceID:   "inv-002",
		TenantID:    "tenant-2",
		PeriodStart: "2026-03-01",
		PeriodEnd:   "2026-04-01",
		Tier:        "pro",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// pro tier: $99 base, 0 usage = $99 total
	if result.Total != 99.0 {
		t.Errorf("expected total 99.0, got %f", result.Total)
	}
}

func TestFinalizeInvoice(t *testing.T) {
	repo := newMockBillingRepo()
	repo.invoices["inv-001"] = &store.InvoiceRecord{ID: "inv-001", Status: "draft"}

	acts := &BillingActivities{Billing: repo}

	if err := acts.FinalizeInvoice(context.Background(), FinalizeInvoiceInput{InvoiceID: "inv-001"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.invoices["inv-001"].Status != "finalized" {
		t.Errorf("expected finalized, got %s", repo.invoices["inv-001"].Status)
	}
}

func TestProcessPayment_ZeroAmount(t *testing.T) {
	acts := &BillingActivities{}

	result, err := acts.ProcessPayment(context.Background(), ProcessPaymentInput{
		InvoiceID: "inv-free",
		TenantID:  "tenant-1",
		Amount:    0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "succeeded" {
		t.Errorf("expected succeeded for zero amount, got %s", result.Status)
	}
}

func TestProcessPayment_PositiveAmount(t *testing.T) {
	acts := &BillingActivities{}

	result, err := acts.ProcessPayment(context.Background(), ProcessPaymentInput{
		InvoiceID: "inv-001",
		TenantID:  "tenant-1",
		Amount:    99.50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "succeeded" {
		t.Errorf("expected succeeded, got %s", result.Status)
	}
	if result.PaymentIntentID == "" {
		t.Error("expected payment intent ID")
	}
}

func TestRecordTierChange(t *testing.T) {
	repo := newMockBillingRepo()
	repo.tenants["tenant-1"] = &store.TenantRecord{ID: "tenant-1", Tier: "free"}

	acts := &BillingActivities{Billing: repo}

	err := acts.RecordTierChange(context.Background(), RecordTierChangeInput{
		TenantID:    "tenant-1",
		FromTier:    "free",
		ToTier:      "pro",
		EffectiveAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.tenants["tenant-1"].Tier != "pro" {
		t.Errorf("expected tier pro, got %s", repo.tenants["tenant-1"].Tier)
	}
	if len(repo.tierEvents) != 1 {
		t.Fatalf("expected 1 tier event, got %d", len(repo.tierEvents))
	}
	if repo.tierEvents[0].ToTier != "pro" {
		t.Errorf("expected event to_tier pro, got %s", repo.tierEvents[0].ToTier)
	}
}

func TestGetTenantInfo(t *testing.T) {
	repo := newMockBillingRepo()
	repo.tenants["tenant-1"] = &store.TenantRecord{ID: "tenant-1", Tier: "enterprise"}

	acts := &BillingActivities{Billing: repo}

	info, err := acts.GetTenantInfo(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Tier != "enterprise" {
		t.Errorf("expected enterprise, got %s", info.Tier)
	}
}

func TestGetTenantInfo_NotFound(t *testing.T) {
	repo := newMockBillingRepo()
	acts := &BillingActivities{Billing: repo}

	_, err := acts.GetTenantInfo(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent tenant")
	}
}
