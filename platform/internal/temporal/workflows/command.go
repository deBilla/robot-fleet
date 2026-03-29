package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

// CommandWorkflowInput is the input for CommandDispatchWorkflow.
type CommandWorkflowInput struct {
	RobotID   string         `json:"robot_id"`
	CommandID int64          `json:"command_id"`
	CmdType   string         `json:"cmd_type"`
	Params    map[string]any `json:"params"`
	TenantID  string         `json:"tenant_id"`
	DedupKey  string         `json:"dedup_key"`
}

// CommandWorkflowResult is the output of CommandDispatchWorkflow.
type CommandWorkflowResult struct {
	CommandID int64  `json:"command_id"`
	Status    string `json:"status"` // "acked", "timeout", "failed"
	RobotID   string `json:"robot_id"`
}

// SignalCommandAck is the signal name for robot command acknowledgment.
const SignalCommandAck = "command-ack"

// CommandDispatchWorkflow orchestrates the full command lifecycle:
// audit (requested) → publish to Redis → wait for robot ack → finalize audit.
func CommandDispatchWorkflow(ctx workflow.Context, input CommandWorkflowInput) (*CommandWorkflowResult, error) {
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})

	// Step 1: Write audit entry (status = "requested")
	err := workflow.ExecuteActivity(actCtx, "WriteCommandAudit", activities.WriteAuditInput{
		CommandID:      input.CommandID,
		RobotID:        input.RobotID,
		TenantID:       input.TenantID,
		CommandType:    input.CmdType,
		Params:         input.Params,
		Status:         "requested",
		IdempotencyKey: input.DedupKey,
	}).Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("write audit: %w", err)
	}

	// Step 2: Publish command to Redis pub/sub (bridges to gRPC stream on ingestion)
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	err = workflow.ExecuteActivity(actCtx, "PublishCommand", activities.PublishCommandInput{
		RobotID:    input.RobotID,
		CommandID:  input.CommandID,
		CmdType:    input.CmdType,
		Params:     input.Params,
		WorkflowID: workflowID,
	}).Get(ctx, nil)
	if err != nil {
		_ = workflow.ExecuteActivity(actCtx, "UpdateCommandAuditStatus", input.CommandID, "dispatch_failed").Get(ctx, nil)
		return &CommandWorkflowResult{CommandID: input.CommandID, Status: "failed", RobotID: input.RobotID}, err
	}

	// Update audit to "dispatched"
	_ = workflow.ExecuteActivity(actCtx, "UpdateCommandAuditStatus", input.CommandID, "dispatched").Get(ctx, nil)

	// Step 3: Wait for ack signal from Kafka-Temporal bridge (or timeout)
	ackCh := workflow.GetSignalChannel(ctx, SignalCommandAck)
	var ackStatus string

	timerCtx, timerCancel := workflow.WithCancel(ctx)
	timerFuture := workflow.NewTimer(timerCtx, 30*time.Second)

	sel := workflow.NewSelector(ctx)
	sel.AddReceive(ackCh, func(ch workflow.ReceiveChannel, more bool) {
		ch.Receive(ctx, &ackStatus)
		timerCancel()
	})
	sel.AddFuture(timerFuture, func(f workflow.Future) {
		ackStatus = "timeout"
	})
	sel.Select(ctx)

	// Step 4: Finalize audit with terminal status
	finalStatus := ackStatus
	if finalStatus == "" || finalStatus == "dispatched" {
		finalStatus = "acked"
	}
	_ = workflow.ExecuteActivity(actCtx, "UpdateCommandAuditStatus", input.CommandID, finalStatus).Get(ctx, nil)

	return &CommandWorkflowResult{
		CommandID: input.CommandID,
		Status:    finalStatus,
		RobotID:   input.RobotID,
	}, nil
}
