package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

// DeploymentWorkflowInput is the input for ModelDeploymentWorkflow.
type DeploymentWorkflowInput struct {
	ModelID string `json:"model_id"`
}

// DeploymentWorkflowResult is the output of ModelDeploymentWorkflow.
type DeploymentWorkflowResult struct {
	ModelID string `json:"model_id"`
	Phase   string `json:"phase"`   // "deployed" or "rolled_back"
	Percent int    `json:"percent"` // final rollout percentage
}

// SignalManualRollback is the signal name for operator-initiated rollback.
const SignalManualRollback = "manual-rollback"

var canaryStages = []int{5, 25, 50, 100}
const observationPeriod = 5 * time.Minute

// ModelDeploymentWorkflow orchestrates a progressive canary deployment:
// staged → canary (5%) → observe → 25% → observe → 50% → observe → 100% (deployed).
// Supports manual rollback via signal at any stage.
func ModelDeploymentWorkflow(ctx workflow.Context, input DeploymentWorkflowInput) (*DeploymentWorkflowResult, error) {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	// Start canary: validate model, find baseline, set status
	var baseline activities.StartCanaryOutput
	err := workflow.ExecuteActivity(actCtx, "StartCanary", input.ModelID).Get(ctx, &baseline)
	if err != nil {
		return nil, err
	}

	rollbackCh := workflow.GetSignalChannel(ctx, SignalManualRollback)
	currentPercent := canaryStages[0]

	for stageIdx := range canaryStages {
		currentPercent = canaryStages[stageIdx]

		// Wait for observation window OR manual rollback signal
		timerCtx, timerCancel := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, observationPeriod)

		manualRollback := false
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(rollbackCh, func(ch workflow.ReceiveChannel, more bool) {
			var reason string
			ch.Receive(ctx, &reason)
			manualRollback = true
			timerCancel()
		})
		sel.AddFuture(timerFuture, func(f workflow.Future) {})
		sel.Select(ctx)

		if manualRollback {
			_ = workflow.ExecuteActivity(actCtx, "RollbackModel", input.ModelID).Get(ctx, nil)
			return &DeploymentWorkflowResult{ModelID: input.ModelID, Phase: "rolled_back", Percent: currentPercent}, nil
		}

		// Evaluate canary metrics against baseline
		var metricsOK bool
		err := workflow.ExecuteActivity(actCtx, "CompareCanaryMetrics", activities.CompareMetricsInput{
			CanaryModelID:   input.ModelID,
			BaselineModelID: baseline.BaselineModelID,
		}).Get(ctx, &metricsOK)
		if err != nil || !metricsOK {
			_ = workflow.ExecuteActivity(actCtx, "RollbackModel", input.ModelID).Get(ctx, nil)
			return &DeploymentWorkflowResult{ModelID: input.ModelID, Phase: "rolled_back", Percent: currentPercent}, nil
		}

		// Expand rollout (or finalize at 100%)
		if currentPercent < 100 {
			_ = workflow.ExecuteActivity(actCtx, "ExpandRollout", activities.ExpandRolloutInput{
				ModelID: input.ModelID,
				Percent: currentPercent,
			}).Get(ctx, nil)
		}
	}

	// Full deployment: set model to deployed, archive baseline
	_ = workflow.ExecuteActivity(actCtx, "FinalizeDeployment", activities.FinalizeInput{
		ModelID:         input.ModelID,
		BaselineModelID: baseline.BaselineModelID,
	}).Get(ctx, nil)

	return &DeploymentWorkflowResult{ModelID: input.ModelID, Phase: "deployed", Percent: 100}, nil
}
