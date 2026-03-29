package activities

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// DeploymentActivities holds dependencies for model deployment Temporal activities.
type DeploymentActivities struct {
	Models store.ModelRepository
}

// StartCanaryOutput is returned by StartCanary.
type StartCanaryOutput struct {
	BaselineModelID string `json:"baseline_model_id"`
}

// CompareMetricsInput is the input for CompareCanaryMetrics.
type CompareMetricsInput struct {
	CanaryModelID   string `json:"canary_model_id"`
	BaselineModelID string `json:"baseline_model_id"`
}

// ExpandRolloutInput is the input for ExpandRollout.
type ExpandRolloutInput struct {
	ModelID string `json:"model_id"`
	Percent int    `json:"percent"`
}

// FinalizeInput is the input for FinalizeDeployment.
type FinalizeInput struct {
	ModelID         string `json:"model_id"`
	BaselineModelID string `json:"baseline_model_id"`
}

// StartCanary validates the model is staged, finds the current baseline, and sets canary status.
func (a *DeploymentActivities) StartCanary(ctx context.Context, modelID string) (*StartCanaryOutput, error) {
	model, err := a.Models.GetModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}
	if model.Status != "staged" {
		return nil, fmt.Errorf("model %s is %s, must be staged", modelID, model.Status)
	}

	deployed, err := a.Models.ListModels(ctx, "deployed")
	if err != nil {
		return nil, fmt.Errorf("list deployed models: %w", err)
	}
	baselineID := ""
	if len(deployed) > 0 {
		baselineID = deployed[0].ID
	}

	if err := a.Models.UpdateModelStatus(ctx, modelID, "canary"); err != nil {
		return nil, fmt.Errorf("set canary status: %w", err)
	}

	slog.Info("canary started", "model", modelID, "baseline", baselineID)
	return &StartCanaryOutput{BaselineModelID: baselineID}, nil
}

const metricDegradationThreshold = 10.0 // percent

// CompareCanaryMetrics compares canary model metrics against baseline.
// Returns true if canary is within acceptable range.
func (a *DeploymentActivities) CompareCanaryMetrics(ctx context.Context, input CompareMetricsInput) (bool, error) {
	canary, err := a.Models.GetModel(ctx, input.CanaryModelID)
	if err != nil {
		return false, fmt.Errorf("get canary model: %w", err)
	}
	if input.BaselineModelID == "" {
		return true, nil // no baseline to compare against
	}
	baseline, err := a.Models.GetModel(ctx, input.BaselineModelID)
	if err != nil {
		return false, fmt.Errorf("get baseline model: %w", err)
	}

	canaryRate := metricFloat(canary.Metrics, "success_rate")
	baselineRate := metricFloat(baseline.Metrics, "success_rate")

	if baselineRate > 0 {
		degradation := (baselineRate - canaryRate) / baselineRate * 100
		if degradation > metricDegradationThreshold {
			slog.Warn("canary degraded", "canary", canaryRate, "baseline", baselineRate, "degradation_pct", degradation)
			return false, nil
		}
	}
	return true, nil
}

// RollbackModel reverts a model to staged status.
func (a *DeploymentActivities) RollbackModel(ctx context.Context, modelID string) error {
	slog.Warn("rolling back model", "model", modelID)
	return a.Models.UpdateModelStatus(ctx, modelID, "staged")
}

// ExpandRollout logs the rollout expansion (placeholder for load balancer weight updates).
func (a *DeploymentActivities) ExpandRollout(ctx context.Context, input ExpandRolloutInput) error {
	slog.Info("expanding rollout", "model", input.ModelID, "percent", input.Percent)
	return nil
}

// FinalizeDeployment sets the canary to deployed and archives the baseline.
func (a *DeploymentActivities) FinalizeDeployment(ctx context.Context, input FinalizeInput) error {
	if err := a.Models.UpdateModelStatus(ctx, input.ModelID, "deployed"); err != nil {
		return fmt.Errorf("deploy model: %w", err)
	}
	if input.BaselineModelID != "" {
		_ = a.Models.UpdateModelStatus(ctx, input.BaselineModelID, "archived")
	}
	slog.Info("model fully deployed", "model", input.ModelID)
	return nil
}

func metricFloat(metrics map[string]any, key string) float64 {
	if v, ok := metrics[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		}
	}
	return 0
}
