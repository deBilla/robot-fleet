package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

// AgentDeploymentInput is the input for AgentDeploymentWorkflow.
type AgentDeploymentInput struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
	Strategy     string `json:"strategy"`
}

// AgentDeploymentResult is the output of AgentDeploymentWorkflow.
type AgentDeploymentResult struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
	FinalStatus  string `json:"final_status"` // "complete" or "rolled_back"
	FinalPercent int    `json:"final_percent"`
}

// Canary stages: 5% → 25% → 50% → 100%.
var agentCanaryStages = []int{5, 25, 50, 100}

// Observation period between canary stages.
const agentObservationPeriod = 5 * time.Minute

// SignalAgentRollback is the signal name for manual rollback.
const SignalAgentRollback = "agent-rollback"

// AgentDeploymentWorkflow orchestrates the full agent deployment lifecycle:
//
//	VALIDATING → CANARY (5%) → observe → check metrics →
//	  PROMOTING (25%) → observe → (50%) → observe → (100%) → COMPLETE
//	  or ROLLED_BACK at any stage.
//
// Every state transition is persisted to Postgres and published to Redis for SSE.
func AgentDeploymentWorkflow(ctx workflow.Context, input AgentDeploymentInput) (*AgentDeploymentResult, error) {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	result := &AgentDeploymentResult{
		DeploymentID: input.DeploymentID,
		AgentID:      input.AgentID,
	}

	rollbackCh := workflow.GetSignalChannel(ctx, SignalAgentRollback)

	// --- Phase 1: VALIDATING ---
	var validation activities.ValidateOutput
	err := workflow.ExecuteActivity(actCtx, "ValidateAgent", activities.ValidateInput{
		DeploymentID: input.DeploymentID,
		AgentID:      input.AgentID,
	}).Get(ctx, &validation)
	if err != nil {
		return nil, err
	}

	if !validation.Approved {
		_ = workflow.ExecuteActivity(actCtx, "RollbackDeployment", activities.RollbackDeploymentInput{
			DeploymentID: input.DeploymentID,
			AgentID:      input.AgentID,
			Reason:       "validation failed",
		}).Get(ctx, nil)
		result.FinalStatus = "rolled_back"
		result.FinalPercent = 0
		return result, nil
	}

	// --- Phase 2: CANARY → PROMOTING → COMPLETE ---
	for _, percent := range agentCanaryStages {
		status := "canary"
		if percent > agentCanaryStages[0] {
			status = "promoting"
		}

		// Transition to the new stage
		_ = workflow.ExecuteActivity(actCtx, "TransitionDeployment", activities.TransitionInput{
			DeploymentID: input.DeploymentID,
			AgentID:      input.AgentID,
			NewStatus:    status,
			Percent:      percent,
		}).Get(ctx, nil)

		// Skip observation for the final 100% stage — finalize immediately after checks
		if percent >= 100 {
			break
		}

		// Observe for the configured period, with manual rollback signal support
		timerCtx, timerCancel := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, agentObservationPeriod)

		manualRollback := false
		var rollbackReason string
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(rollbackCh, func(ch workflow.ReceiveChannel, more bool) {
			ch.Receive(ctx, &rollbackReason)
			manualRollback = true
			timerCancel()
		})
		sel.AddFuture(timerFuture, func(f workflow.Future) {})
		sel.Select(ctx)

		if manualRollback {
			reason := "manual rollback"
			if rollbackReason != "" {
				reason = rollbackReason
			}
			_ = workflow.ExecuteActivity(actCtx, "RollbackDeployment", activities.RollbackDeploymentInput{
				DeploymentID: input.DeploymentID,
				AgentID:      input.AgentID,
				Reason:       reason,
			}).Get(ctx, nil)
			result.FinalStatus = "rolled_back"
			result.FinalPercent = percent
			return result, nil
		}

		// Check metrics (safety violations, task failures, e-stops)
		var metrics activities.CheckMetricsOutput
		err := workflow.ExecuteActivity(actCtx, "CheckDeploymentMetrics", activities.CheckMetricsInput{
			DeploymentID: input.DeploymentID,
			AgentID:      input.AgentID,
		}).Get(ctx, &metrics)
		if err != nil || !metrics.Healthy {
			reason := "metrics degraded"
			if err != nil {
				reason = "metrics check failed: " + err.Error()
			}
			_ = workflow.ExecuteActivity(actCtx, "RollbackDeployment", activities.RollbackDeploymentInput{
				DeploymentID: input.DeploymentID,
				AgentID:      input.AgentID,
				Reason:       reason,
			}).Get(ctx, nil)
			result.FinalStatus = "rolled_back"
			result.FinalPercent = percent
			return result, nil
		}
	}

	// --- Phase 3: COMPLETE ---
	_ = workflow.ExecuteActivity(actCtx, "FinalizeAgentDeployment", activities.FinalizeDeploymentInput{
		DeploymentID: input.DeploymentID,
		AgentID:      input.AgentID,
	}).Get(ctx, nil)

	result.FinalStatus = "complete"
	result.FinalPercent = 100
	return result, nil
}
