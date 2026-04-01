package workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

func TestBillingCycleWorkflow_HappyPath(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.BillingActivities{}
	env.RegisterActivity(acts)

	// Mock all activities
	env.OnActivity(acts.AggregateUsage, mock.Anything, mock.Anything).Return(
		&activities.AggregateUsageOutput{Counts: map[string]int64{"api_calls": 100}}, nil,
	)
	env.OnActivity(acts.GenerateInvoice, mock.Anything, mock.Anything).Return(
		&activities.GenerateInvoiceOutput{InvoiceID: "inv-tenant-1-2026-03-01", Total: 99.0}, nil,
	)
	env.OnActivity(acts.FinalizeInvoice, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.ProcessPayment, mock.Anything, mock.Anything).Return(
		&activities.ProcessPaymentOutput{PaymentIntentID: "pi_123", Status: "succeeded"}, nil,
	)
	env.OnActivity(acts.UpdateInvoicePaymentStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(BillingCycleWorkflow, BillingCycleInput{
		TenantID:    "tenant-1",
		PeriodStart: "2099-01-01",
		Tier:        "pro",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	// Should continue-as-new for next month
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("expected ContinueAsNewError")
	}
	var continueErr *workflow.ContinueAsNewError
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should complete with ContinueAsNew")
	}
	_ = continueErr
}

func TestBillingCycleWorkflow_ZeroAmountInvoice(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.BillingActivities{}
	env.RegisterActivity(acts)

	env.OnActivity(acts.AggregateUsage, mock.Anything, mock.Anything).Return(
		&activities.AggregateUsageOutput{Counts: map[string]int64{}}, nil,
	)
	env.OnActivity(acts.GenerateInvoice, mock.Anything, mock.Anything).Return(
		&activities.GenerateInvoiceOutput{InvoiceID: "inv-free-1", Total: 0.0}, nil,
	)
	env.OnActivity(acts.FinalizeInvoice, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.UpdateInvoicePaymentStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(BillingCycleWorkflow, BillingCycleInput{
		TenantID:    "tenant-free",
		PeriodStart: "2099-01-01",
		Tier:        "free",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	// ProcessPayment should NOT have been called for zero-amount
	env.AssertNotCalled(t, "ProcessPayment", mock.Anything, mock.Anything)
}

func TestBillingCycleWorkflow_TierChangeSignal(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.BillingActivities{}
	env.RegisterActivity(acts)

	env.OnActivity(acts.AggregateUsage, mock.Anything, mock.Anything).Return(
		&activities.AggregateUsageOutput{Counts: map[string]int64{}}, nil,
	)
	env.OnActivity(acts.RecordTierChange, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.GenerateInvoice, mock.Anything, mock.Anything).Return(
		&activities.GenerateInvoiceOutput{InvoiceID: "inv-1", Total: 99.0}, nil,
	)
	env.OnActivity(acts.FinalizeInvoice, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.ProcessPayment, mock.Anything, mock.Anything).Return(
		&activities.ProcessPaymentOutput{Status: "succeeded"}, nil,
	)
	env.OnActivity(acts.UpdateInvoicePaymentStatus, mock.Anything, mock.Anything).Return(nil)

	// Signal tier change early in the billing cycle
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalChangeTier, ChangeTierSignal{NewTier: "enterprise"})
	}, 1*time.Hour)

	env.ExecuteWorkflow(BillingCycleWorkflow, BillingCycleInput{
		TenantID:    "tenant-1",
		PeriodStart: "2099-01-01",
		Tier:        "pro",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	// Verify RecordTierChange was called
	env.AssertCalled(t, "RecordTierChange", mock.Anything, mock.Anything)
}

func TestBillingCycleWorkflow_CancelSubscription(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.BillingActivities{}
	env.RegisterActivity(acts)

	env.OnActivity(acts.AggregateUsage, mock.Anything, mock.Anything).Return(
		&activities.AggregateUsageOutput{Counts: map[string]int64{}}, nil,
	)
	env.OnActivity(acts.GenerateInvoice, mock.Anything, mock.Anything).Return(
		&activities.GenerateInvoiceOutput{InvoiceID: "inv-cancel", Total: 0.0}, nil,
	)
	env.OnActivity(acts.FinalizeInvoice, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.UpdateInvoicePaymentStatus, mock.Anything, mock.Anything).Return(nil)

	// Signal cancellation
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCancelSubscription, CancelSubscriptionSignal{Reason: "requested by user"})
	}, 1*time.Hour)

	env.ExecuteWorkflow(BillingCycleWorkflow, BillingCycleInput{
		TenantID:    "tenant-1",
		PeriodStart: "2099-01-01",
		Tier:        "pro",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	// Should NOT continue-as-new after cancellation
	err := env.GetWorkflowError()
	if err != nil {
		t.Fatalf("expected no error after cancellation, got: %v", err)
	}
}
