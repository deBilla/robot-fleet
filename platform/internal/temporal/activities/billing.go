package activities

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimuthu/robot-fleet/internal/billing"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// BillingActivities holds dependencies for billing Temporal activities.
type BillingActivities struct {
	Billing store.BillingRepository
	Cache   store.CacheStore
}

// Metrics aggregated by the billing system.
var billingMetrics = []string{"api_calls", "inference_calls"}

// AggregateUsageInput is the input for AggregateUsage.
type AggregateUsageInput struct {
	TenantID string `json:"tenant_id"`
	Date     string `json:"date"` // YYYY-MM-DD
}

// AggregateUsageOutput is the result of AggregateUsage.
type AggregateUsageOutput struct {
	TenantID string         `json:"tenant_id"`
	Date     string         `json:"date"`
	Counts   map[string]int64 `json:"counts"`
}

// AggregateUsage reads Redis usage counters and persists them to Postgres.
func (a *BillingActivities) AggregateUsage(ctx context.Context, input AggregateUsageInput) (*AggregateUsageOutput, error) {
	date, err := time.Parse("2006-01-02", input.Date)
	if err != nil {
		return nil, fmt.Errorf("parse date %s: %w", input.Date, err)
	}

	counts := make(map[string]int64, len(billingMetrics))
	for _, metric := range billingMetrics {
		count, err := a.Cache.GetUsageCounter(ctx, input.TenantID, metric, input.Date)
		if err != nil {
			// Redis key may not exist (no usage that day) — treat as 0
			count = 0
		}
		counts[metric] = count

		if err := a.Billing.UpsertDailyUsage(ctx, input.TenantID, date, metric, count); err != nil {
			return nil, fmt.Errorf("upsert daily usage %s/%s: %w", input.TenantID, metric, err)
		}
	}

	slog.Info("aggregated usage", "tenant", input.TenantID, "date", input.Date, "counts", counts)
	return &AggregateUsageOutput{
		TenantID: input.TenantID,
		Date:     input.Date,
		Counts:   counts,
	}, nil
}

// GenerateInvoiceInput is the input for GenerateInvoice.
type GenerateInvoiceInput struct {
	InvoiceID   string `json:"invoice_id"`
	TenantID    string `json:"tenant_id"`
	PeriodStart string `json:"period_start"` // YYYY-MM-DD
	PeriodEnd   string `json:"period_end"`   // YYYY-MM-DD
	Tier        string `json:"tier"`
}

// GenerateInvoiceOutput is the result of GenerateInvoice.
type GenerateInvoiceOutput struct {
	InvoiceID string  `json:"invoice_id"`
	Total     float64 `json:"total"`
}

// GenerateInvoice computes and persists an invoice from aggregated daily usage.
func (a *BillingActivities) GenerateInvoice(ctx context.Context, input GenerateInvoiceInput) (*GenerateInvoiceOutput, error) {
	start, err := time.Parse("2006-01-02", input.PeriodStart)
	if err != nil {
		return nil, fmt.Errorf("parse period start: %w", err)
	}
	end, err := time.Parse("2006-01-02", input.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("parse period end: %w", err)
	}

	records, err := a.Billing.GetDailyUsage(ctx, input.TenantID, start, end)
	if err != nil {
		return nil, fmt.Errorf("get daily usage: %w", err)
	}

	// Sum usage across all days
	var totalAPICalls, totalInferenceCalls int64
	for _, r := range records {
		switch r.Metric {
		case "api_calls":
			totalAPICalls += r.Count
		case "inference_calls":
			totalInferenceCalls += r.Count
		}
	}

	// Reuse existing pure invoice calculation
	tier := billing.GetPricingTier(input.Tier)
	invoice := billing.CalculateInvoice(tier, totalAPICalls, totalInferenceCalls)

	inv := &store.InvoiceRecord{
		ID:          input.InvoiceID,
		TenantID:    input.TenantID,
		PeriodStart: start,
		PeriodEnd:   end,
		Tier:        input.Tier,
		LineItems: map[string]any{
			"base_charge":       invoice.BaseCharge,
			"api_calls":         totalAPICalls,
			"api_overage":       invoice.APIOverage,
			"inference_calls":   totalInferenceCalls,
			"inference_overage": invoice.InferenceOverage,
		},
		Subtotal:  invoice.Total,
		Total:     invoice.Total,
		Currency:  "USD",
		Status:    "draft",
		CreatedAt: time.Now(),
	}

	if err := a.Billing.CreateInvoice(ctx, inv); err != nil {
		return nil, fmt.Errorf("create invoice: %w", err)
	}

	slog.Info("invoice generated", "invoice", input.InvoiceID, "tenant", input.TenantID, "total", invoice.Total)
	return &GenerateInvoiceOutput{InvoiceID: input.InvoiceID, Total: invoice.Total}, nil
}

// FinalizeInvoiceInput is the input for FinalizeInvoice.
type FinalizeInvoiceInput struct {
	InvoiceID string `json:"invoice_id"`
}

// FinalizeInvoice marks an invoice as finalized (ready for payment).
func (a *BillingActivities) FinalizeInvoice(ctx context.Context, input FinalizeInvoiceInput) error {
	if err := a.Billing.UpdateInvoiceStatus(ctx, input.InvoiceID, "finalized"); err != nil {
		return fmt.Errorf("finalize invoice %s: %w", input.InvoiceID, err)
	}
	slog.Info("invoice finalized", "invoice", input.InvoiceID)
	return nil
}

// ProcessPaymentInput is the input for ProcessPayment.
type ProcessPaymentInput struct {
	InvoiceID string  `json:"invoice_id"`
	TenantID  string  `json:"tenant_id"`
	Amount    float64 `json:"amount"`
}

// ProcessPaymentOutput is the result of ProcessPayment.
type ProcessPaymentOutput struct {
	PaymentIntentID string `json:"payment_intent_id"`
	Status          string `json:"status"` // "succeeded", "failed"
}

// PermanentPaymentError indicates a non-retryable payment failure.
type PermanentPaymentError struct {
	Reason string
}

func (e *PermanentPaymentError) Error() string {
	return fmt.Sprintf("permanent payment failure: %s", e.Reason)
}

// ProcessPayment attempts to collect payment for an invoice.
// Placeholder implementation — future integration with Stripe.
func (a *BillingActivities) ProcessPayment(ctx context.Context, input ProcessPaymentInput) (*ProcessPaymentOutput, error) {
	// Skip payment for zero-amount invoices
	if input.Amount <= 0 {
		return &ProcessPaymentOutput{
			PaymentIntentID: "",
			Status:          "succeeded",
		}, nil
	}

	// Placeholder: In production, call Stripe PaymentIntent.Create() here.
	// Non-retryable errors (card declined, invalid account) should return &PermanentPaymentError{}.
	slog.Info("processing payment (placeholder)", "invoice", input.InvoiceID, "amount", input.Amount)

	return &ProcessPaymentOutput{
		PaymentIntentID: fmt.Sprintf("pi_placeholder_%s", input.InvoiceID),
		Status:          "succeeded",
	}, nil
}

// UpdateInvoicePaymentStatusInput is the input for UpdateInvoicePaymentStatus.
type UpdateInvoicePaymentStatusInput struct {
	InvoiceID string `json:"invoice_id"`
	Status    string `json:"status"` // "paid" or "failed"
}

// UpdateInvoicePaymentStatus updates an invoice based on payment outcome.
func (a *BillingActivities) UpdateInvoicePaymentStatus(ctx context.Context, input UpdateInvoicePaymentStatusInput) error {
	if err := a.Billing.UpdateInvoiceStatus(ctx, input.InvoiceID, input.Status); err != nil {
		return fmt.Errorf("update invoice payment status %s: %w", input.InvoiceID, err)
	}
	slog.Info("invoice payment status updated", "invoice", input.InvoiceID, "status", input.Status)
	return nil
}

// RecordTierChangeInput is the input for RecordTierChange.
type RecordTierChangeInput struct {
	TenantID        string  `json:"tenant_id"`
	FromTier        string  `json:"from_tier"`
	ToTier          string  `json:"to_tier"`
	EffectiveAt     time.Time `json:"effective_at"`
	ProrationAmount float64 `json:"proration_amount"`
}

// RecordTierChange updates the tenant's tier and records the change event.
func (a *BillingActivities) RecordTierChange(ctx context.Context, input RecordTierChangeInput) error {
	if err := a.Billing.UpdateTenantTier(ctx, input.TenantID, input.ToTier); err != nil {
		return fmt.Errorf("update tenant tier: %w", err)
	}

	event := &store.TierChangeEventRecord{
		TenantID:        input.TenantID,
		FromTier:        input.FromTier,
		ToTier:          input.ToTier,
		EffectiveAt:     input.EffectiveAt,
		ProrationAmount: input.ProrationAmount,
	}
	if err := a.Billing.CreateTierChangeEvent(ctx, event); err != nil {
		return fmt.Errorf("record tier change event: %w", err)
	}

	slog.Info("tier change recorded", "tenant", input.TenantID, "from", input.FromTier, "to", input.ToTier)
	return nil
}

// GetTenantInfoOutput is the result of GetTenantInfo.
type GetTenantInfoOutput struct {
	TenantID string `json:"tenant_id"`
	Tier     string `json:"tier"`
}

// GetTenantInfo fetches the current tenant billing info.
func (a *BillingActivities) GetTenantInfo(ctx context.Context, tenantID string) (*GetTenantInfoOutput, error) {
	tenant, err := a.Billing.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant %s: %w", tenantID, err)
	}
	return &GetTenantInfoOutput{
		TenantID: tenant.ID,
		Tier:     tenant.Tier,
	}, nil
}
