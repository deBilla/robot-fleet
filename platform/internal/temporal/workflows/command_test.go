package workflows

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

func TestCommandDispatchWorkflow_HappyPath(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.CommandActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.WriteCommandAudit, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.PublishCommand, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.UpdateCommandAuditStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Signal ack shortly after workflow starts
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCommandAck, "dispatched")
	}, 0)

	env.ExecuteWorkflow(CommandDispatchWorkflow, CommandWorkflowInput{
		RobotID:   "robot-0001",
		CommandID: 123456,
		CmdType:   "move",
		Params:    map[string]any{"x": 1.0},
		TenantID:  "tenant-dev",
		DedupKey:  "abc123",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result CommandWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Status != "acked" {
		t.Errorf("expected status 'acked', got '%s'", result.Status)
	}
	if result.RobotID != "robot-0001" {
		t.Errorf("expected robot-0001, got %s", result.RobotID)
	}
}

func TestCommandDispatchWorkflow_WithInference(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	cmdActs := &activities.CommandActivities{}
	inferActs := &activities.InferenceActivities{}
	env.RegisterActivity(cmdActs)
	env.RegisterActivity(inferActs)
	env.OnActivity(cmdActs.WriteCommandAudit, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(cmdActs.PublishCommand, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(cmdActs.UpdateCommandAuditStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(inferActs.RunInference, mock.Anything, mock.Anything).Return(
		&activities.InferenceResult{CmdType: "walk", Params: map[string]any{}}, nil,
	)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCommandAck, "dispatched")
	}, 0)

	env.ExecuteWorkflow(CommandDispatchWorkflow, CommandWorkflowInput{
		RobotID:              "robot-0001",
		CommandID:            999,
		CmdType:              "",
		Params:               nil,
		TenantID:             "tenant-dev",
		DedupKey:             "inf123",
		WithInference:        true,
		InferenceInstruction: "walk forward",
		InferenceModelID:     "groot-n1-v1.5",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result CommandWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Status != "acked" {
		t.Errorf("expected acked, got %s", result.Status)
	}
}

func TestCommandDispatchWorkflow_WithInference_ApplyActions(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	cmdActs := &activities.CommandActivities{}
	inferActs := &activities.InferenceActivities{}
	env.RegisterActivity(cmdActs)
	env.RegisterActivity(inferActs)
	env.OnActivity(cmdActs.WriteCommandAudit, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(cmdActs.UpdateCommandAuditStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Inference returns apply_actions with torques
	inferResult := &activities.InferenceResult{
		CmdType: "apply_actions",
		Params:  map[string]any{"actions": []map[string]any{{"joint": "hip", "torque": 0.5}}},
	}
	env.OnActivity(inferActs.RunInference, mock.Anything, mock.Anything).Return(inferResult, nil)

	// Capture the PublishCommand input to verify inference result was used
	var publishInput activities.PublishCommandInput
	env.OnActivity(cmdActs.PublishCommand, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		publishInput = args.Get(1).(activities.PublishCommandInput)
	})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCommandAck, "dispatched")
	}, 0)

	env.ExecuteWorkflow(CommandDispatchWorkflow, CommandWorkflowInput{
		RobotID:              "robot-0001",
		CommandID:            1000,
		CmdType:              "",
		TenantID:             "tenant-dev",
		DedupKey:             "inf456",
		WithInference:        true,
		InferenceInstruction: "do a backflip",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}
	if publishInput.CmdType != "apply_actions" {
		t.Errorf("expected PublishCommand with apply_actions, got %s", publishInput.CmdType)
	}
}

func TestCommandDispatchWorkflow_Timeout(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.CommandActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.WriteCommandAudit, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.PublishCommand, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.UpdateCommandAuditStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// No signal → timeout

	env.ExecuteWorkflow(CommandDispatchWorkflow, CommandWorkflowInput{
		RobotID:   "robot-0002",
		CommandID: 789,
		CmdType:   "dance",
		Params:    map[string]any{},
		TenantID:  "tenant-dev",
		DedupKey:  "def456",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	var result CommandWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Status != "timeout" {
		t.Errorf("expected status 'timeout', got '%s'", result.Status)
	}
}
