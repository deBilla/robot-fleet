package service

import (
	"context"

	"go.temporal.io/sdk/client"

	"github.com/dimuthu/robot-fleet/internal/billing"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// PricingTier is an alias for the shared billing package type.
type PricingTier = billing.PricingTier

// Invoice is an alias for the shared billing package type.
type Invoice = billing.Invoice

// GetPricingTier delegates to the billing package.
func GetPricingTier(name string) PricingTier {
	return billing.GetPricingTier(name)
}

// CalculateInvoice delegates to the billing package.
func CalculateInvoice(tier PricingTier, apiCalls, inferenceCalls int64) Invoice {
	return billing.CalculateInvoice(tier, apiCalls, inferenceCalls)
}

// BillingService handles billing operations.
type BillingService interface {
	GetInvoice(ctx context.Context, tenantID, tier string) (*Invoice, error)
	ListTiers() []PricingTier
	GetPersistedInvoice(ctx context.Context, invoiceID string) (*store.InvoiceRecord, error)
	ListInvoices(ctx context.Context, tenantID string, limit int) ([]*store.InvoiceRecord, error)
	GetSubscription(ctx context.Context, tenantID string) (*store.TenantRecord, error)
	ChangeTier(ctx context.Context, tenantID, newTier string) error
	StartBillingCycle(ctx context.Context, tenantID, tier string) error
	RetryPayment(ctx context.Context, tenantID string) error
}

// NewBillingService creates a new billing service backed by Temporal workflows.
func NewBillingService(billingRepo store.BillingRepository, cache store.CacheStore, tc client.Client) BillingService {
	return NewTemporalBillingService(billingRepo, cache, tc)
}
