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
