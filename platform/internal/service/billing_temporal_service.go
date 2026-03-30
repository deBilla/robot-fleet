package service

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/dimuthu/robot-fleet/internal/billing"
	"github.com/dimuthu/robot-fleet/internal/store"
	temporalpkg "github.com/dimuthu/robot-fleet/internal/temporal"
	"github.com/dimuthu/robot-fleet/internal/temporal/workflows"
)

// TemporalBillingService implements BillingService with Temporal workflow integration.
type TemporalBillingService struct {
	billing  store.BillingRepository
	cache    store.CacheStore
	temporal client.Client
}

// NewTemporalBillingService creates a billing service backed by Temporal workflows.
func NewTemporalBillingService(billing store.BillingRepository, cache store.CacheStore, tc client.Client) *TemporalBillingService {
	return &TemporalBillingService{billing: billing, cache: cache, temporal: tc}
}

func (s *TemporalBillingService) GetInvoice(ctx context.Context, tenantID, tierName string) (*Invoice, error) {
	tier := GetPricingTier(tierName)
	date := time.Now().Format("2006-01-02")

	apiCalls, _ := s.cache.GetUsageCounter(ctx, tenantID, "api_calls", date)
	inferenceCalls, _ := s.cache.GetUsageCounter(ctx, tenantID, "inference_calls", date)

	invoice := CalculateInvoice(tier, apiCalls, inferenceCalls)
	invoice.TenantID = tenantID
	invoice.Period = fmt.Sprintf("%s (daily)", date)

	return &invoice, nil
}

func (s *TemporalBillingService) ListTiers() []PricingTier {
	return billing.ListTiers()
}

// GetPersistedInvoice retrieves an invoice from the database.
func (s *TemporalBillingService) GetPersistedInvoice(ctx context.Context, invoiceID string) (*store.InvoiceRecord, error) {
	inv, err := s.billing.GetInvoice(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("get persisted invoice %s: %w", invoiceID, err)
	}
	return inv, nil
}

// ListInvoices returns persisted invoices for a tenant.
func (s *TemporalBillingService) ListInvoices(ctx context.Context, tenantID string, limit int) ([]*store.InvoiceRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	invoices, err := s.billing.ListInvoices(ctx, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list invoices for %s: %w", tenantID, err)
	}
	return invoices, nil
}

// GetSubscription returns the current subscription info for a tenant.
func (s *TemporalBillingService) GetSubscription(ctx context.Context, tenantID string) (*store.TenantRecord, error) {
	tenant, err := s.billing.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get subscription for %s: %w", tenantID, err)
	}
	return tenant, nil
}

// ChangeTier signals the running billing workflow to change the tenant's tier.
func (s *TemporalBillingService) ChangeTier(ctx context.Context, tenantID, newTier string) error {
	if !billing.ValidTier(newTier) {
		return fmt.Errorf("unknown tier: %s", newTier)
	}

	now := time.Now()
	workflowID := fmt.Sprintf("billing-cycle-%s-%s", tenantID, now.Format("2006-01"))

	err := s.temporal.SignalWorkflow(ctx, workflowID, "", workflows.SignalChangeTier, workflows.ChangeTierSignal{
		NewTier: newTier,
	})
	if err != nil {
		return fmt.Errorf("signal tier change for %s: %w", tenantID, err)
	}
	return nil
}

// StartBillingCycle starts a BillingCycleWorkflow for a tenant.
func (s *TemporalBillingService) StartBillingCycle(ctx context.Context, tenantID, tier string) error {
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	workflowID := fmt.Sprintf("billing-cycle-%s-%s", tenantID, now.Format("2006-01"))

	_, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: temporalpkg.TaskQueueBilling,
	}, workflows.BillingCycleWorkflow, workflows.BillingCycleInput{
		TenantID:    tenantID,
		PeriodStart: periodStart,
		Tier:        tier,
	})
	if err != nil {
		return fmt.Errorf("start billing cycle for %s: %w", tenantID, err)
	}
	return nil
}

// RetryPayment signals the billing workflow to retry a failed payment.
func (s *TemporalBillingService) RetryPayment(ctx context.Context, tenantID string) error {
	now := time.Now()
	workflowID := fmt.Sprintf("billing-cycle-%s-%s", tenantID, now.Format("2006-01"))

	err := s.temporal.SignalWorkflow(ctx, workflowID, "", workflows.SignalRetryPayment, struct{}{})
	if err != nil {
		return fmt.Errorf("signal retry payment for %s: %w", tenantID, err)
	}
	return nil
}
