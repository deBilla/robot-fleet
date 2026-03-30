package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimuthu/robot-fleet/internal/billing"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// AdminService defines the business logic interface for admin operations.
type AdminService interface {
	CreateTenant(ctx context.Context, req CreateTenantRequest) (*CreateTenantResult, error)
	GetTenant(ctx context.Context, tenantID string) (*store.TenantRecord, error)
	ListTenants(ctx context.Context) ([]*store.TenantRecord, error)
	UpdateTenant(ctx context.Context, tenantID string, req UpdateTenantRequest) error
	CreateAPIKey(ctx context.Context, tenantID string, req CreateAPIKeyRequest) (*CreateAPIKeyResult, error)
	ListAPIKeys(ctx context.Context, tenantID string) ([]*store.APIKeyRecord, error)
	RevokeAPIKey(ctx context.Context, keyHash string) error
}

// CreateTenantRequest holds the input for creating a new tenant.
type CreateTenantRequest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Tier         string `json:"tier"`
	BillingEmail string `json:"billing_email"`
}

// CreateTenantResult holds the output of tenant creation.
type CreateTenantResult struct {
	Tenant *store.TenantRecord `json:"tenant"`
	APIKey string              `json:"api_key"`
}

// UpdateTenantRequest holds the input for updating a tenant.
type UpdateTenantRequest struct {
	Name         string `json:"name"`
	BillingEmail string `json:"billing_email"`
}

// CreateAPIKeyRequest holds the input for creating an API key.
type CreateAPIKeyRequest struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	RateLimit int    `json:"rate_limit"`
	ExpiresIn string `json:"expires_in"` // duration string, e.g. "720h"
}

// CreateAPIKeyResult holds the output of key creation.
type CreateAPIKeyResult struct {
	PlaintextKey string              `json:"api_key"`
	Record       *store.APIKeyRecord `json:"record"`
}

type adminService struct {
	billing    store.BillingRepository
	keys       store.APIKeyRepository
	billingSvc *TemporalBillingService // nullable — billing cycle start is best-effort
}

// NewAdminService creates a new admin service.
func NewAdminService(billingRepo store.BillingRepository, keyRepo store.APIKeyRepository, billingSvc *TemporalBillingService) AdminService {
	return &adminService{billing: billingRepo, keys: keyRepo, billingSvc: billingSvc}
}

func (s *adminService) CreateTenant(ctx context.Context, req CreateTenantRequest) (*CreateTenantResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("tenant name is required")
	}

	// Generate ID if not provided
	if req.ID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		req.ID = fmt.Sprintf("tenant-%s", hex.EncodeToString(b))
	}

	// Default tier
	if req.Tier == "" {
		req.Tier = "free"
	}
	if !billing.ValidTier(req.Tier) {
		return nil, fmt.Errorf("unknown tier: %s", req.Tier)
	}

	now := time.Now()
	tenant := &store.TenantRecord{
		ID:           req.ID,
		Name:         req.Name,
		Tier:         req.Tier,
		BillingEmail: req.BillingEmail,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.billing.UpsertTenant(ctx, tenant); err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}

	// Start billing cycle (best-effort — Temporal may be unavailable)
	if s.billingSvc != nil {
		if err := s.billingSvc.StartBillingCycle(ctx, tenant.ID, tenant.Tier); err != nil {
			slog.Warn("failed to start billing cycle for new tenant", "tenant", tenant.ID, "error", err)
		}
	}

	// Generate initial admin API key
	keyResult, err := s.CreateAPIKey(ctx, tenant.ID, CreateAPIKeyRequest{
		Name: "Initial admin key",
		Role: "admin",
	})
	if err != nil {
		return nil, fmt.Errorf("create initial api key: %w", err)
	}

	return &CreateTenantResult{
		Tenant: tenant,
		APIKey: keyResult.PlaintextKey,
	}, nil
}

func (s *adminService) GetTenant(ctx context.Context, tenantID string) (*store.TenantRecord, error) {
	tenant, err := s.billing.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant %s: %w", tenantID, err)
	}
	return tenant, nil
}

func (s *adminService) ListTenants(ctx context.Context) ([]*store.TenantRecord, error) {
	return s.billing.ListTenants(ctx)
}

func (s *adminService) UpdateTenant(ctx context.Context, tenantID string, req UpdateTenantRequest) error {
	tenant, err := s.billing.GetTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("tenant %s not found: %w", tenantID, err)
	}

	if req.Name != "" {
		tenant.Name = req.Name
	}
	if req.BillingEmail != "" {
		tenant.BillingEmail = req.BillingEmail
	}
	tenant.UpdatedAt = time.Now()

	return s.billing.UpsertTenant(ctx, tenant)
}

const (
	apiKeyPrefix    = "fos_"
	apiKeyRandBytes = 32
	defaultRateLimit = 100
)

func (s *adminService) CreateAPIKey(ctx context.Context, tenantID string, req CreateAPIKeyRequest) (*CreateAPIKeyResult, error) {
	if req.Name == "" {
		req.Name = "Unnamed key"
	}
	if req.Role == "" {
		req.Role = "developer"
	}
	if req.RateLimit <= 0 {
		req.RateLimit = defaultRateLimit
	}

	// Generate plaintext key
	randBytes := make([]byte, apiKeyRandBytes)
	if _, err := rand.Read(randBytes); err != nil {
		return nil, fmt.Errorf("generate random key: %w", err)
	}
	plaintext := apiKeyPrefix + hex.EncodeToString(randBytes)

	// Hash for storage
	hash := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(hash[:])

	// Parse expiry
	var expiresAt *time.Time
	if req.ExpiresIn != "" {
		dur, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			return nil, fmt.Errorf("invalid expires_in: %w", err)
		}
		t := time.Now().Add(dur)
		expiresAt = &t
	}

	record := &store.APIKeyRecord{
		KeyHash:   keyHash,
		TenantID:  tenantID,
		Name:      req.Name,
		Role:      req.Role,
		RateLimit: req.RateLimit,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}

	if err := s.keys.CreateAPIKey(ctx, record); err != nil {
		return nil, fmt.Errorf("store api key: %w", err)
	}

	return &CreateAPIKeyResult{
		PlaintextKey: plaintext,
		Record:       record,
	}, nil
}

func (s *adminService) ListAPIKeys(ctx context.Context, tenantID string) ([]*store.APIKeyRecord, error) {
	return s.keys.ListAPIKeys(ctx, tenantID)
}

func (s *adminService) RevokeAPIKey(ctx context.Context, keyHash string) error {
	return s.keys.RevokeAPIKey(ctx, keyHash)
}
