package activities

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// DeploymentActivities holds dependencies for model deployment Temporal activities.
type DeploymentActivities struct {
	Models store.ModelRepository
	Robots store.RobotRepository
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

// RollbackInput is the input for RollbackModel.
type RollbackInput struct {
	CanaryModelID   string `json:"canary_model_id"`
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

// ExpandRollout assigns the canary model to a percentage of active robots
// using deterministic hashing for stable canary bucket selection.
func (a *DeploymentActivities) ExpandRollout(ctx context.Context, input ExpandRolloutInput) error {
	since := time.Now().Add(-1 * time.Hour)
	robots, err := a.Robots.ListAllActiveRobots(ctx, since, 10000)
	if err != nil {
		return fmt.Errorf("list active robots: %w", err)
	}
	if len(robots) == 0 {
		slog.Info("no active robots for rollout", "model", input.ModelID)
		return nil
	}

	assigned := 0
	for _, r := range robots {
		if shouldAssignModel(r.ID, input.Percent) {
			if err := a.Robots.UpdateRobotInferenceModel(ctx, r.ID, input.ModelID); err != nil {
				slog.Error("failed to assign model to robot", "robot", r.ID, "error", err)
			} else {
				assigned++
			}
		}
	}

	slog.Info("expanded rollout", "model", input.ModelID, "percent", input.Percent,
		"total_robots", len(robots), "assigned", assigned)
	return nil
}

// RollbackModel reverts a model to staged and reassigns affected robots to the baseline.
func (a *DeploymentActivities) RollbackModel(ctx context.Context, input RollbackInput) error {
	slog.Warn("rolling back model", "canary", input.CanaryModelID, "baseline", input.BaselineModelID)
	if err := a.Models.UpdateModelStatus(ctx, input.CanaryModelID, "staged"); err != nil {
		return fmt.Errorf("rollback model status: %w", err)
	}

	// Reassign robots on canary model back to baseline
	robots, err := a.Robots.ListRobotsByInferenceModel(ctx, input.CanaryModelID)
	if err != nil {
		slog.Error("failed to list canary robots for rollback", "error", err)
		return nil // non-fatal: status already rolled back
	}
	for _, r := range robots {
		_ = a.Robots.UpdateRobotInferenceModel(ctx, r.ID, input.BaselineModelID)
	}
	slog.Info("rollback complete", "robots_reassigned", len(robots))
	return nil
}

// FinalizeDeployment sets the canary to deployed, archives the baseline,
// and assigns the model to all active robots.
func (a *DeploymentActivities) FinalizeDeployment(ctx context.Context, input FinalizeInput) error {
	if err := a.Models.UpdateModelStatus(ctx, input.ModelID, "deployed"); err != nil {
		return fmt.Errorf("deploy model: %w", err)
	}
	if input.BaselineModelID != "" {
		_ = a.Models.UpdateModelStatus(ctx, input.BaselineModelID, "archived")
	}

	// Assign deployed model to ALL active robots
	since := time.Now().Add(-1 * time.Hour)
	robots, err := a.Robots.ListAllActiveRobots(ctx, since, 10000)
	if err != nil {
		slog.Error("failed to list robots for finalize", "error", err)
	} else {
		for _, r := range robots {
			_ = a.Robots.UpdateRobotInferenceModel(ctx, r.ID, input.ModelID)
		}
	}

	slog.Info("model fully deployed", "model", input.ModelID, "robots_assigned", len(robots))
	return nil
}

// shouldAssignModel uses deterministic hashing to decide if a robot falls
// within the canary percentage. The same robot always lands in the same
// bucket, so the canary group only grows through stages (5% ⊂ 25% ⊂ 50%).
func shouldAssignModel(robotID string, percent int) bool {
	h := fnv.New32a()
	h.Write([]byte(robotID))
	return int(h.Sum32()%100) < percent
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
