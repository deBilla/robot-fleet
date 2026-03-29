package workflows

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

func TestModelDeploymentWorkflow_FullRollout(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.DeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.StartCanary, mock.Anything, mock.Anything).Return(&activities.StartCanaryOutput{
		BaselineModelID: "model-old-v1",
	}, nil)
	env.OnActivity(acts.CompareCanaryMetrics, mock.Anything, mock.Anything).Return(true, nil)
	env.OnActivity(acts.ExpandRollout, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.FinalizeDeployment, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.RollbackModel, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(ModelDeploymentWorkflow, DeploymentWorkflowInput{
		ModelID: "model-new-v2",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result DeploymentWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Phase != "deployed" {
		t.Errorf("expected phase 'deployed', got '%s'", result.Phase)
	}
	if result.Percent != 100 {
		t.Errorf("expected 100%%, got %d%%", result.Percent)
	}
}

func TestModelDeploymentWorkflow_MetricsRollback(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.DeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.StartCanary, mock.Anything, mock.Anything).Return(&activities.StartCanaryOutput{
		BaselineModelID: "model-old-v1",
	}, nil)
	env.OnActivity(acts.CompareCanaryMetrics, mock.Anything, mock.Anything).Return(false, nil)
	env.OnActivity(acts.RollbackModel, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.ExpandRollout, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.FinalizeDeployment, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(ModelDeploymentWorkflow, DeploymentWorkflowInput{
		ModelID: "model-new-v2",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	var result DeploymentWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Phase != "rolled_back" {
		t.Errorf("expected phase 'rolled_back', got '%s'", result.Phase)
	}
}

func TestModelDeploymentWorkflow_ManualRollback(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	acts := &activities.DeploymentActivities{}
	env.RegisterActivity(acts)
	env.OnActivity(acts.StartCanary, mock.Anything, mock.Anything).Return(&activities.StartCanaryOutput{
		BaselineModelID: "model-old-v1",
	}, nil)
	env.OnActivity(acts.RollbackModel, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.CompareCanaryMetrics, mock.Anything, mock.Anything).Return(true, nil)
	env.OnActivity(acts.ExpandRollout, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.FinalizeDeployment, mock.Anything, mock.Anything).Return(nil)

	// Send manual rollback signal immediately
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalManualRollback, "operator requested")
	}, 0)

	env.ExecuteWorkflow(ModelDeploymentWorkflow, DeploymentWorkflowInput{
		ModelID: "model-new-v2",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should have completed")
	}

	var result DeploymentWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Phase != "rolled_back" {
		t.Errorf("expected phase 'rolled_back', got '%s'", result.Phase)
	}
}
