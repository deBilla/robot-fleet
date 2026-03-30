package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

// BillingCycleInput is the input for BillingCycleWorkflow.
type BillingCycleInput struct {
	TenantID    string `json:"tenant_id"`
	PeriodStart string `json:"period_start"` // YYYY-MM-DD (1st of the month)
	Tier        string `json:"tier"`
}

// Signal names for the billing workflow.
const (
	SignalChangeTier          = "change-tier"
	SignalCancelSubscription  = "cancel-subscription"
	SignalRetryPayment        = "retry-payment"
)

// ChangeTierSignal is the payload for a tier change signal.
type ChangeTierSignal struct {
	NewTier string `json:"new_tier"`
}

// CancelSubscriptionSignal is the payload for a subscription cancellation signal.
type CancelSubscriptionSignal struct {
	Reason string `json:"reason"`
}

// Billing cycle constants.
const (
	AggregationInterval = 6 * time.Hour
	DunningInterval     = 3 * 24 * time.Hour
	MaxDunningAttempts  = 3
)

// BillingCycleWorkflow is a long-running per-tenant workflow that manages the monthly
// billing lifecycle: usage aggregation, invoice generation, payment, and tier changes.
// It continues-as-new at each month boundary to prevent unbounded history growth.
func BillingCycleWorkflow(ctx workflow.Context, input BillingCycleInput) error {
	logger := workflow.GetLogger(ctx)

	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    5,
		},
	})

	periodStart, err := time.Parse("2006-01-02", input.PeriodStart)
	if err != nil {
		return fmt.Errorf("parse period start: %w", err)
	}
	periodEnd := periodStart.AddDate(0, 1, 0) // 1st of next month

	currentTier := input.Tier
	cancelled := false

	// Signal channels
	tierCh := workflow.GetSignalChannel(ctx, SignalChangeTier)
	cancelCh := workflow.GetSignalChannel(ctx, SignalCancelSubscription)

	// Phase 1: Usage aggregation loop (every AggregationInterval until period end)
	for {
		now := workflow.Now(ctx)
		if !now.Before(periodEnd) {
			break
		}

		// Sleep until next aggregation (or period end, whichever is first)
		sleepDuration := AggregationInterval
		if now.Add(sleepDuration).After(periodEnd) {
			sleepDuration = periodEnd.Sub(now)
		}

		// Use selector to handle signals during sleep
		timerCtx, timerCancel := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, sleepDuration)

		sel := workflow.NewSelector(ctx)

		sel.AddFuture(timerFuture, func(f workflow.Future) {
			// Timer fired — run aggregation
		})

		sel.AddReceive(tierCh, func(ch workflow.ReceiveChannel, more bool) {
			var signal ChangeTierSignal
			ch.Receive(ctx, &signal)
			logger.Info("tier change signal received", "new_tier", signal.NewTier)

			oldTier := currentTier
			currentTier = signal.NewTier

			_ = workflow.ExecuteActivity(actCtx, "RecordTierChange", activities.RecordTierChangeInput{
				TenantID:    input.TenantID,
				FromTier:    oldTier,
				ToTier:      signal.NewTier,
				EffectiveAt: workflow.Now(ctx),
			}).Get(ctx, nil)

			timerCancel()
		})

		sel.AddReceive(cancelCh, func(ch workflow.ReceiveChannel, more bool) {
			var signal CancelSubscriptionSignal
			ch.Receive(ctx, &signal)
			logger.Info("subscription cancelled", "reason", signal.Reason)
			cancelled = true
			timerCancel()
		})

		sel.Select(ctx)

		if cancelled {
			break
		}

		// Run aggregation for today and yesterday (covers Redis TTL edge cases)
		now = workflow.Now(ctx)
		today := now.Format("2006-01-02")
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

		_ = workflow.ExecuteActivity(actCtx, "AggregateUsage", activities.AggregateUsageInput{
			TenantID: input.TenantID,
			Date:     today,
		}).Get(ctx, nil)

		_ = workflow.ExecuteActivity(actCtx, "AggregateUsage", activities.AggregateUsageInput{
			TenantID: input.TenantID,
			Date:     yesterday,
		}).Get(ctx, nil)
	}

	// Phase 2: Final aggregation — cover any remaining days in the period
	now := workflow.Now(ctx)
	finalDate := now.Format("2006-01-02")
	_ = workflow.ExecuteActivity(actCtx, "AggregateUsage", activities.AggregateUsageInput{
		TenantID: input.TenantID,
		Date:     finalDate,
	}).Get(ctx, nil)

	// Phase 3: Generate invoice
	invoiceID := fmt.Sprintf("inv-%s-%s", input.TenantID, input.PeriodStart)
	var invoiceResult activities.GenerateInvoiceOutput
	err = workflow.ExecuteActivity(actCtx, "GenerateInvoice", activities.GenerateInvoiceInput{
		InvoiceID:   invoiceID,
		TenantID:    input.TenantID,
		PeriodStart: input.PeriodStart,
		PeriodEnd:   periodEnd.Format("2006-01-02"),
		Tier:        currentTier,
	}).Get(ctx, &invoiceResult)
	if err != nil {
		return fmt.Errorf("generate invoice: %w", err)
	}

	// Phase 4: Finalize invoice
	err = workflow.ExecuteActivity(actCtx, "FinalizeInvoice", activities.FinalizeInvoiceInput{
		InvoiceID: invoiceID,
	}).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("finalize invoice: %w", err)
	}

	// Phase 5: Process payment
	paymentSuccess := false
	if invoiceResult.Total > 0 {
		var payResult activities.ProcessPaymentOutput

		paymentActCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:        2 * time.Second,
				BackoffCoefficient:     2.0,
				MaximumAttempts:        3,
				NonRetryableErrorTypes: []string{"PermanentPaymentError"},
			},
		})

		err = workflow.ExecuteActivity(paymentActCtx, "ProcessPayment", activities.ProcessPaymentInput{
			InvoiceID: invoiceID,
			TenantID:  input.TenantID,
			Amount:    invoiceResult.Total,
		}).Get(ctx, &payResult)

		if err == nil && payResult.Status == "succeeded" {
			paymentSuccess = true
		}

		if paymentSuccess {
			_ = workflow.ExecuteActivity(actCtx, "UpdateInvoicePaymentStatus", activities.UpdateInvoicePaymentStatusInput{
				InvoiceID: invoiceID,
				Status:    "paid",
			}).Get(ctx, nil)
		} else {
			// Phase 5b: Dunning — retry failed payments
			paymentSuccess = runDunning(ctx, actCtx, paymentActCtx, invoiceID, input.TenantID, invoiceResult.Total)
		}

		if !paymentSuccess {
			_ = workflow.ExecuteActivity(actCtx, "UpdateInvoicePaymentStatus", activities.UpdateInvoicePaymentStatusInput{
				InvoiceID: invoiceID,
				Status:    "failed",
			}).Get(ctx, nil)
			logger.Warn("payment failed after dunning", "invoice", invoiceID, "tenant", input.TenantID)
		}
	} else {
		// Zero-amount invoice — mark as paid immediately
		_ = workflow.ExecuteActivity(actCtx, "UpdateInvoicePaymentStatus", activities.UpdateInvoicePaymentStatusInput{
			InvoiceID: invoiceID,
			Status:    "paid",
		}).Get(ctx, nil)
	}

	// If subscription cancelled, stop the cycle
	if cancelled {
		logger.Info("billing cycle ended due to cancellation", "tenant", input.TenantID)
		return nil
	}

	// Phase 6: Continue-as-new for the next billing period
	// Drain any pending signals before continuing
	for tierCh.ReceiveAsync(&ChangeTierSignal{}) {
	}
	for cancelCh.ReceiveAsync(&CancelSubscriptionSignal{}) {
	}

	return workflow.NewContinueAsNewError(ctx, BillingCycleWorkflow, BillingCycleInput{
		TenantID:    input.TenantID,
		PeriodStart: periodEnd.Format("2006-01-02"),
		Tier:        currentTier,
	})
}

// runDunning retries payment collection with increasing delays.
func runDunning(ctx, actCtx, paymentActCtx workflow.Context, invoiceID, tenantID string, amount float64) bool {
	retryCh := workflow.GetSignalChannel(ctx, SignalRetryPayment)

	for attempt := range MaxDunningAttempts {
		// Wait for dunning interval or manual retry signal
		timerCtx, timerCancel := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, DunningInterval)

		sel := workflow.NewSelector(ctx)
		manualRetry := false

		sel.AddFuture(timerFuture, func(f workflow.Future) {
			// Timer expired — proceed to retry
		})
		sel.AddReceive(retryCh, func(ch workflow.ReceiveChannel, more bool) {
			var dummy struct{}
			ch.Receive(ctx, &dummy)
			manualRetry = true
			timerCancel()
		})
		sel.Select(ctx)

		_ = manualRetry // both paths lead to retry
		_ = attempt

		var payResult activities.ProcessPaymentOutput
		err := workflow.ExecuteActivity(paymentActCtx, "ProcessPayment", activities.ProcessPaymentInput{
			InvoiceID: invoiceID,
			TenantID:  tenantID,
			Amount:    amount,
		}).Get(ctx, &payResult)

		if err == nil && payResult.Status == "succeeded" {
			_ = workflow.ExecuteActivity(actCtx, "UpdateInvoicePaymentStatus", activities.UpdateInvoicePaymentStatusInput{
				InvoiceID: invoiceID,
				Status:    "paid",
			}).Get(ctx, nil)
			return true
		}
	}
	return false
}
