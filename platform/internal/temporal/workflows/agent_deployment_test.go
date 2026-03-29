package workflows

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

func TestAgentDeploymentWorkflow_FullRollout(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.AgentDeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.ValidateAgent, mock.Anything, mock.Anything).Return(&activities.ValidateOutput{
		Approved: true,
		Report:   map[string]any{"checks_passed": 2, "checks_total": 2},
	}, nil)
	env.OnActivity(acts.TransitionDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.CheckDeploymentMetrics, mock.Anything, mock.Anything).Return(&activities.CheckMetricsOutput{
		Healthy: true,
	}, nil)
	env.OnActivity(acts.RollbackDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.FinalizeAgentDeployment, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(AgentDeploymentWorkflow, AgentDeploymentInput{
		DeploymentID: "deploy-001",
		AgentID:      "agent-001",
		Strategy:     "canary",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result AgentDeploymentResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.FinalStatus != "complete" {
		t.Errorf("expected 'complete', got '%s'", result.FinalStatus)
	}
	if result.FinalPercent != 100 {
		t.Errorf("expected 100%%, got %d%%", result.FinalPercent)
	}
}

func TestAgentDeploymentWorkflow_ValidationFailed(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.AgentDeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.ValidateAgent, mock.Anything, mock.Anything).Return(&activities.ValidateOutput{
		Approved: false,
		Report:   map[string]any{"checks_passed": 0, "checks_total": 2},
	}, nil)
	env.OnActivity(acts.RollbackDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.TransitionDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.CheckDeploymentMetrics, mock.Anything, mock.Anything).Return(&activities.CheckMetricsOutput{Healthy: true}, nil)
	env.OnActivity(acts.FinalizeAgentDeployment, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(AgentDeploymentWorkflow, AgentDeploymentInput{
		DeploymentID: "deploy-002",
		AgentID:      "agent-002",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	var result AgentDeploymentResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.FinalStatus != "rolled_back" {
		t.Errorf("expected 'rolled_back', got '%s'", result.FinalStatus)
	}
	if result.FinalPercent != 0 {
		t.Errorf("expected 0%%, got %d%%", result.FinalPercent)
	}
}

func TestAgentDeploymentWorkflow_MetricsDegradation(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.AgentDeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.ValidateAgent, mock.Anything, mock.Anything).Return(&activities.ValidateOutput{
		Approved: true,
	}, nil)
	env.OnActivity(acts.TransitionDeployment, mock.Anything, mock.Anything).Return(nil)
	// Metrics fail at the first check (canary 5%)
	env.OnActivity(acts.CheckDeploymentMetrics, mock.Anything, mock.Anything).Return(&activities.CheckMetricsOutput{
		Healthy:             false,
		SafetyViolationRate: 0.01,
	}, nil)
	env.OnActivity(acts.RollbackDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.FinalizeAgentDeployment, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(AgentDeploymentWorkflow, AgentDeploymentInput{
		DeploymentID: "deploy-003",
		AgentID:      "agent-003",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	var result AgentDeploymentResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.FinalStatus != "rolled_back" {
		t.Errorf("expected 'rolled_back', got '%s'", result.FinalStatus)
	}
	if result.FinalPercent != 5 {
		t.Errorf("expected rollback at 5%%, got %d%%", result.FinalPercent)
	}
}

func TestAgentDeploymentWorkflow_ManualRollback(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.AgentDeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.ValidateAgent, mock.Anything, mock.Anything).Return(&activities.ValidateOutput{
		Approved: true,
	}, nil)
	env.OnActivity(acts.TransitionDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.CheckDeploymentMetrics, mock.Anything, mock.Anything).Return(&activities.CheckMetricsOutput{Healthy: true}, nil)
	env.OnActivity(acts.RollbackDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.FinalizeAgentDeployment, mock.Anything, mock.Anything).Return(nil)

	// Send manual rollback signal during the first observation period
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalAgentRollback, "operator: safety concern")
	}, 0)

	env.ExecuteWorkflow(AgentDeploymentWorkflow, AgentDeploymentInput{
		DeploymentID: "deploy-004",
		AgentID:      "agent-004",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	var result AgentDeploymentResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.FinalStatus != "rolled_back" {
		t.Errorf("expected 'rolled_back', got '%s'", result.FinalStatus)
	}
}
