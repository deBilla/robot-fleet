package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// mockAPIKeyRepo implements store.APIKeyRepository for testing.
type mockAPIKeyRepo struct {
	keys map[string]*store.APIKeyRecord
}

func newMockAPIKeyRepo() *mockAPIKeyRepo {
	return &mockAPIKeyRepo{keys: make(map[string]*store.APIKeyRecord)}
}

func (m *mockAPIKeyRepo) CreateAPIKey(_ context.Context, k *store.APIKeyRecord) error {
	if _, exists := m.keys[k.KeyHash]; exists {
		return fmt.Errorf("duplicate key")
	}
	m.keys[k.KeyHash] = k
	return nil
}

func (m *mockAPIKeyRepo) GetAPIKey(_ context.Context, keyHash string) (*store.APIKeyRecord, error) {
	k, ok := m.keys[keyHash]
	if !ok || k.Revoked {
		return nil, fmt.Errorf("key not found")
	}
	return k, nil
}

func (m *mockAPIKeyRepo) ListAPIKeys(_ context.Context, tenantID string) ([]*store.APIKeyRecord, error) {
	var out []*store.APIKeyRecord
	for _, k := range m.keys {
		if k.TenantID == tenantID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *mockAPIKeyRepo) RevokeAPIKey(_ context.Context, keyHash string) error {
	k, ok := m.keys[keyHash]
	if !ok {
		return fmt.Errorf("key not found")
	}
	k.Revoked = true
	return nil
}

// mockAdminBillingRepo implements store.BillingRepository for admin tests.
type mockAdminBillingRepo struct {
	tenants map[string]*store.TenantRecord
}

func newMockAdminBillingRepo() *mockAdminBillingRepo {
	return &mockAdminBillingRepo{tenants: make(map[string]*store.TenantRecord)}
}

func (m *mockAdminBillingRepo) UpsertTenant(_ context.Context, t *store.TenantRecord) error {
	m.tenants[t.ID] = t
	return nil
}

func (m *mockAdminBillingRepo) GetTenant(_ context.Context, id string) (*store.TenantRecord, error) {
	t, ok := m.tenants[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return t, nil
}

func (m *mockAdminBillingRepo) ListTenants(_ context.Context) ([]*store.TenantRecord, error) {
	var out []*store.TenantRecord
	for _, t := range m.tenants {
		out = append(out, t)
	}
	return out, nil
}

func (m *mockAdminBillingRepo) UpdateTenantTier(_ context.Context, id, tier string) error {
	t, ok := m.tenants[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	t.Tier = tier
	return nil
}

func (m *mockAdminBillingRepo) UpsertDailyUsage(_ context.Context, _ string, _ time.Time, _ string, _ int64) error {
	return nil
}
func (m *mockAdminBillingRepo) GetDailyUsage(_ context.Context, _ string, _, _ time.Time) ([]*store.UsageDailyRecord, error) {
	return nil, nil
}
func (m *mockAdminBillingRepo) CreateInvoice(_ context.Context, _ *store.InvoiceRecord) error {
	return nil
}
func (m *mockAdminBillingRepo) GetInvoice(_ context.Context, _ string) (*store.InvoiceRecord, error) {
	return nil, fmt.Errorf("not found")
}
func (m *mockAdminBillingRepo) ListInvoices(_ context.Context, _ string, _ int) ([]*store.InvoiceRecord, error) {
	return nil, nil
}
func (m *mockAdminBillingRepo) UpdateInvoiceStatus(_ context.Context, _, _ string) error { return nil }
func (m *mockAdminBillingRepo) CreateTierChangeEvent(_ context.Context, _ *store.TierChangeEventRecord) error {
	return nil
}

func TestCreateTenant_Success(t *testing.T) {
	billingRepo := newMockAdminBillingRepo()
	keyRepo := newMockAPIKeyRepo()
	svc := NewAdminService(billingRepo, keyRepo, nil)

	result, err := svc.CreateTenant(context.Background(), CreateTenantRequest{
		Name:         "Test Corp",
		Tier:         "pro",
		BillingEmail: "admin@test.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Tenant.Name != "Test Corp" {
		t.Errorf("expected name Test Corp, got %s", result.Tenant.Name)
	}
	if result.Tenant.Tier != "pro" {
		t.Errorf("expected tier pro, got %s", result.Tenant.Tier)
	}
	if !strings.HasPrefix(result.APIKey, "fos_") {
		t.Errorf("expected API key with fos_ prefix, got %s", result.APIKey)
	}
	if result.Tenant.ID == "" {
		t.Error("expected generated tenant ID")
	}

	// Verify tenant stored
	if len(billingRepo.tenants) != 1 {
		t.Errorf("expected 1 tenant, got %d", len(billingRepo.tenants))
	}

	// Verify initial API key stored
	if len(keyRepo.keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keyRepo.keys))
	}
}

func TestCreateTenant_DefaultTier(t *testing.T) {
	svc := NewAdminService(newMockAdminBillingRepo(), newMockAPIKeyRepo(), nil)

	result, err := svc.CreateTenant(context.Background(), CreateTenantRequest{
		Name: "Free Corp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tenant.Tier != "free" {
		t.Errorf("expected default tier free, got %s", result.Tenant.Tier)
	}
}

func TestCreateTenant_MissingName(t *testing.T) {
	svc := NewAdminService(newMockAdminBillingRepo(), newMockAPIKeyRepo(), nil)

	_, err := svc.CreateTenant(context.Background(), CreateTenantRequest{})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestCreateTenant_InvalidTier(t *testing.T) {
	svc := NewAdminService(newMockAdminBillingRepo(), newMockAPIKeyRepo(), nil)

	_, err := svc.CreateTenant(context.Background(), CreateTenantRequest{
		Name: "Test",
		Tier: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for invalid tier")
	}
}

func TestCreateAPIKey_HashMatchesStored(t *testing.T) {
	keyRepo := newMockAPIKeyRepo()
	svc := NewAdminService(newMockAdminBillingRepo(), keyRepo, nil)

	result, err := svc.CreateAPIKey(context.Background(), "tenant-1", CreateAPIKeyRequest{
		Name: "Test key",
		Role: "developer",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify hash matches
	hash := sha256.Sum256([]byte(result.PlaintextKey))
	expectedHash := hex.EncodeToString(hash[:])

	if result.Record.KeyHash != expectedHash {
		t.Errorf("stored hash doesn't match sha256 of plaintext key")
	}

	// Verify stored in repo
	stored, err := keyRepo.GetAPIKey(context.Background(), expectedHash)
	if err != nil {
		t.Fatalf("key not found in repo: %v", err)
	}
	if stored.TenantID != "tenant-1" {
		t.Errorf("expected tenant-1, got %s", stored.TenantID)
	}
	if stored.Role != "developer" {
		t.Errorf("expected developer role, got %s", stored.Role)
	}
}

func TestCreateAPIKey_DefaultValues(t *testing.T) {
	svc := NewAdminService(newMockAdminBillingRepo(), newMockAPIKeyRepo(), nil)

	result, err := svc.CreateAPIKey(context.Background(), "tenant-1", CreateAPIKeyRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Record.Name != "Unnamed key" {
		t.Errorf("expected default name, got %s", result.Record.Name)
	}
	if result.Record.Role != "developer" {
		t.Errorf("expected default role developer, got %s", result.Record.Role)
	}
	if result.Record.RateLimit != 100 {
		t.Errorf("expected default rate limit 100, got %d", result.Record.RateLimit)
	}
}

func TestCreateAPIKey_WithExpiry(t *testing.T) {
	svc := NewAdminService(newMockAdminBillingRepo(), newMockAPIKeyRepo(), nil)

	result, err := svc.CreateAPIKey(context.Background(), "tenant-1", CreateAPIKeyRequest{
		Name:      "Temp key",
		ExpiresIn: "24h",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Record.ExpiresAt == nil {
		t.Fatal("expected expiry to be set")
	}
	if time.Until(*result.Record.ExpiresAt) < 23*time.Hour {
		t.Error("expected expiry roughly 24h from now")
	}
}

func TestRevokeAPIKey(t *testing.T) {
	keyRepo := newMockAPIKeyRepo()
	keyRepo.keys["hash123"] = &store.APIKeyRecord{KeyHash: "hash123", TenantID: "t1"}

	svc := NewAdminService(newMockAdminBillingRepo(), keyRepo, nil)

	if err := svc.RevokeAPIKey(context.Background(), "hash123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !keyRepo.keys["hash123"].Revoked {
		t.Error("expected key to be revoked")
	}
}

func TestListAPIKeys(t *testing.T) {
	keyRepo := newMockAPIKeyRepo()
	keyRepo.keys["h1"] = &store.APIKeyRecord{KeyHash: "h1", TenantID: "t1", Name: "key1"}
	keyRepo.keys["h2"] = &store.APIKeyRecord{KeyHash: "h2", TenantID: "t1", Name: "key2", Revoked: true}
	keyRepo.keys["h3"] = &store.APIKeyRecord{KeyHash: "h3", TenantID: "t2", Name: "other"}

	svc := NewAdminService(newMockAdminBillingRepo(), keyRepo, nil)

	keys, err := svc.ListAPIKeys(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys for t1, got %d", len(keys))
	}
}

func TestUpdateTenant(t *testing.T) {
	billingRepo := newMockAdminBillingRepo()
	billingRepo.tenants["t1"] = &store.TenantRecord{ID: "t1", Name: "Old Name"}

	svc := NewAdminService(billingRepo, newMockAPIKeyRepo(), nil)

	err := svc.UpdateTenant(context.Background(), "t1", UpdateTenantRequest{
		Name:         "New Name",
		BillingEmail: "new@test.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if billingRepo.tenants["t1"].Name != "New Name" {
		t.Errorf("expected New Name, got %s", billingRepo.tenants["t1"].Name)
	}
}
