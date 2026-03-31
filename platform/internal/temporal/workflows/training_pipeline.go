package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
)

// TrainingPipelineInput is the input for TrainingPipelineWorkflow.
type TrainingPipelineInput struct {
	JobID          string `json:"job_id"`
	Timesteps      int    `json:"timesteps"`       // default: 2000000
	EnvID          string `json:"env_id"`           // default: FleetOS-Humanoid-v1
	UseKatib       bool   `json:"use_katib"`        // HPO via Katib
	AutoDeploy     bool   `json:"auto_deploy"`      // auto canary deploy if evaluation passes
	BaseModel      string `json:"base_model"`       // S3 key for fine-tuning, empty = from scratch
	FromExperience bool   `json:"from_experience"`  // include collected experience
}

// TrainingPipelineResult is the output of TrainingPipelineWorkflow.
type TrainingPipelineResult struct {
	JobID       string  `json:"job_id"`
	ModelID     string  `json:"model_id"`
	Phase       string  `json:"phase"`     // "trained", "registered", "deployed", "failed", "gated"
	MeanReward  float64 `json:"mean_reward"`
	Improvement float64 `json:"improvement_pct"`
}

// SignalRetrain is the signal name to trigger retraining.
const SignalRetrain = "retrain"

// TrainingPipelineWorkflow orchestrates the full ML lifecycle:
// collect experience → submit Kubeflow training → evaluate → register → canary deploy.
//
// Can be triggered by:
// 1. Manual API call (POST /api/v1/locomotion/jobs)
// 2. Signal from monitoring when success_rate drops below threshold
// 3. Cron schedule (weekly retraining)
func TrainingPipelineWorkflow(ctx workflow.Context, input TrainingPipelineInput) (*TrainingPipelineResult, error) {
	logger := workflow.GetLogger(ctx)

	if input.Timesteps == 0 {
		input.Timesteps = 2_000_000
	}
	if input.EnvID == "" {
		input.EnvID = "FleetOS-Humanoid-v1"
	}
	if input.JobID == "" {
		input.JobID = fmt.Sprintf("train-%d", workflow.Now(ctx).UnixNano())
	}

	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	longActCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 4 * time.Hour, // training can take hours
		HeartbeatTimeout:    5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	})

	result := &TrainingPipelineResult{JobID: input.JobID}

	// Step 1: Check experience data availability
	experiencePrefix := ""
	if input.FromExperience {
		var expStats activities.CollectExperienceOutput
		err := workflow.ExecuteActivity(actCtx, "CollectExperienceStats", activities.CollectExperienceInput{
			S3Bucket: "fleetos-models",
			Prefix:   "experience/",
		}).Get(ctx, &expStats)
		if err == nil && expStats.TransitionCount > 1000 {
			experiencePrefix = "experience/"
			logger.Info("experience data available", "transitions", expStats.TransitionCount)
		}
	}

	// Step 2: Find current baseline model for comparison
	baseModel := input.BaseModel
	baselineModelID := "" // caller provides via input.BaseModel, or empty = train from scratch

	// Step 3: Submit training to Kubeflow
	logger.Info("submitting training", "job_id", input.JobID, "timesteps", input.Timesteps, "katib", input.UseKatib)

	var trainResult activities.SubmitKubeflowRunOutput
	err := workflow.ExecuteActivity(longActCtx, "SubmitKubeflowRun", activities.SubmitKubeflowRunInput{
		JobID:          input.JobID,
		Timesteps:      input.Timesteps,
		EnvID:          input.EnvID,
		BaseModel:      baseModel,
		FromExperience: experiencePrefix,
		UseKatib:       input.UseKatib,
	}).Get(ctx, &trainResult)
	if err != nil {
		result.Phase = "failed"
		return result, fmt.Errorf("training submission failed: %w", err)
	}

	logger.Info("training submitted", "run_id", trainResult.RunID, "model_path", trainResult.ModelPath)

	// Step 4: Evaluate trained model against baseline
	var evalResult activities.EvaluateModelOutput
	err = workflow.ExecuteActivity(actCtx, "EvaluateTrainedModel", activities.EvaluateModelInput{
		ModelArtifactURL: trainResult.ModelPath,
		BaselineModelID:  baselineModelID,
		EvalEpisodes:     20,
	}).Get(ctx, &evalResult)
	if err != nil {
		result.Phase = "failed"
		return result, fmt.Errorf("evaluation failed: %w", err)
	}

	result.MeanReward = evalResult.MeanReward
	result.Improvement = evalResult.Improvement

	// Gate: only proceed if model passes evaluation threshold
	if !evalResult.PassesGate {
		result.Phase = "gated"
		logger.Warn("model did not pass evaluation gate",
			"mean_reward", evalResult.MeanReward,
			"baseline", evalResult.BaselineReward,
		)
		_ = workflow.ExecuteActivity(actCtx, "NotifyTrainingComplete", input.JobID, false).Get(ctx, nil)
		return result, nil
	}

	// Step 5: Register model in registry
	var modelID string
	version := fmt.Sprintf("v%d", workflow.Now(ctx).Unix())
	err = workflow.ExecuteActivity(actCtx, "RegisterTrainedModel", activities.RegisterTrainedModelInput{
		Name:        "ppo-lab",
		Version:     version,
		ArtifactURL: trainResult.ModelPath,
		MeanReward:  evalResult.MeanReward,
		Environment: input.EnvID,
	}).Get(ctx, &modelID)
	if err != nil {
		result.Phase = "failed"
		return result, fmt.Errorf("model registration failed: %w", err)
	}

	result.ModelID = modelID
	result.Phase = "registered"
	logger.Info("model registered", "model_id", modelID)

	// Step 6: Auto-deploy via canary (if enabled)
	if input.AutoDeploy {
		logger.Info("starting canary deployment", "model_id", modelID)

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("deploy-%s", modelID),
		})

		var deployResult DeploymentWorkflowResult
		err = workflow.ExecuteChildWorkflow(childCtx, ModelDeploymentWorkflow, DeploymentWorkflowInput{
			ModelID: modelID,
		}).Get(ctx, &deployResult)

		if err != nil || deployResult.Phase == "rolled_back" {
			result.Phase = "registered" // deployed failed but model is still in registry
			logger.Warn("canary deployment failed or rolled back", "model_id", modelID)
		} else {
			result.Phase = "deployed"
			logger.Info("model deployed via canary", "model_id", modelID)
		}
	}

	_ = workflow.ExecuteActivity(actCtx, "NotifyTrainingComplete", modelID, result.Phase == "deployed").Get(ctx, nil)

	return result, nil
}
