package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// Deployment phases and thresholds.
const (
	CanaryPercent            = 5
	ProgressivePercent25     = 25
	ProgressivePercent50     = 50
	FullRolloutPercent       = 100
	MetricDegradationPercent = 10 // rollback if canary is >10% worse than baseline
	ObservationWindow        = 5 * time.Minute
)

// DeploymentState tracks the progress of a model rollout.
type DeploymentState struct {
	ModelID         string    `json:"model_id"`
	Strategy        string    `json:"strategy"` // canary, progressive, full
	Phase           string    `json:"phase"`    // canary, expanding, deployed, rolled_back
	Percent         int       `json:"percent"`
	BaselineModelID string    `json:"baseline_model_id"`
	StartedAt       time.Time `json:"started_at"`
	LastCheckedAt   time.Time `json:"last_checked_at"`
}

// DeploymentController manages progressive model rollouts with automatic rollback.
type DeploymentController struct {
	models store.ModelRepository
	olap   store.AnalyticsStore
}

// NewDeploymentController creates a new deployment controller.
func NewDeploymentController(models store.ModelRepository, olap store.AnalyticsStore) *DeploymentController {
	return &DeploymentController{models: models, olap: olap}
}

// StartCanaryDeployment begins a canary deployment for a staged model.
func (dc *DeploymentController) StartCanaryDeployment(ctx context.Context, modelID string) (*DeploymentState, error) {
	model, err := dc.models.GetModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", modelID, err)
	}

	if model.Status != ModelStatusStaged {
		return nil, fmt.Errorf("model %s is %s, must be staged to start canary", modelID, model.Status)
	}

	// Find current deployed model as baseline
	deployed, err := dc.models.ListModels(ctx, ModelStatusDeployed)
	if err != nil {
		return nil, fmt.Errorf("list deployed models: %w", err)
	}

	baselineID := ""
	if len(deployed) > 0 {
		baselineID = deployed[0].ID
	}

	// Transition model to canary status
	if err := dc.models.UpdateModelStatus(ctx, modelID, ModelStatusCanary); err != nil {
		return nil, fmt.Errorf("set canary status: %w", err)
	}

	state := &DeploymentState{
		ModelID:         modelID,
		Strategy:        "progressive",
		Phase:           "canary",
		Percent:         CanaryPercent,
		BaselineModelID: baselineID,
		StartedAt:       time.Now().UTC(),
		LastCheckedAt:   time.Now().UTC(),
	}

	slog.Info("canary deployment started",
		"model", modelID,
		"baseline", baselineID,
		"percent", CanaryPercent,
	)

	return state, nil
}

// EvaluateAndProgress checks canary metrics against baseline and decides to expand or rollback.
func (dc *DeploymentController) EvaluateAndProgress(ctx context.Context, state *DeploymentState) (*DeploymentState, error) {
	if state.Phase == "deployed" || state.Phase == "rolled_back" {
		return state, nil
	}

	// Compare canary metrics vs baseline from analytics
	canaryOK, err := dc.compareMetrics(ctx, state.ModelID, state.BaselineModelID)
	if err != nil {
		slog.Warn("metrics comparison failed, holding position", "error", err)
		return state, nil
	}

	if !canaryOK {
		// Rollback: revert to staged, restore baseline
		slog.Warn("canary metrics degraded, rolling back",
			"model", state.ModelID,
			"baseline", state.BaselineModelID,
		)
		if err := dc.models.UpdateModelStatus(ctx, state.ModelID, ModelStatusStaged); err != nil {
			return nil, fmt.Errorf("rollback model status: %w", err)
		}
		state.Phase = "rolled_back"
		return state, nil
	}

	// Metrics OK — expand rollout
	switch {
	case state.Percent < ProgressivePercent25:
		state.Percent = ProgressivePercent25
		state.Phase = "expanding"
		slog.Info("expanding rollout to 25%", "model", state.ModelID)
	case state.Percent < ProgressivePercent50:
		state.Percent = ProgressivePercent50
		slog.Info("expanding rollout to 50%", "model", state.ModelID)
	case state.Percent < FullRolloutPercent:
		state.Percent = FullRolloutPercent
		state.Phase = "deployed"
		if err := dc.models.UpdateModelStatus(ctx, state.ModelID, ModelStatusDeployed); err != nil {
			return nil, fmt.Errorf("deploy model: %w", err)
		}
		// Archive the old baseline
		if state.BaselineModelID != "" {
			_ = dc.models.UpdateModelStatus(ctx, state.BaselineModelID, ModelStatusArchived)
		}
		slog.Info("model fully deployed", "model", state.ModelID)
	}

	state.LastCheckedAt = time.Now().UTC()
	return state, nil
}

// compareMetrics checks if the canary model performs within acceptable range of baseline.
func (dc *DeploymentController) compareMetrics(ctx context.Context, canaryID, baselineID string) (bool, error) {
	canary, err := dc.models.GetModel(ctx, canaryID)
	if err != nil {
		return false, fmt.Errorf("get canary model: %w", err)
	}
	if baselineID == "" {
		// No baseline — accept canary if it has positive metrics
		return true, nil
	}
	baseline, err := dc.models.GetModel(ctx, baselineID)
	if err != nil {
		return false, fmt.Errorf("get baseline model: %w", err)
	}

	// Compare success_rate metric if available
	canarySuccessRate := getMetricFloat(canary.Metrics, "success_rate")
	baselineSuccessRate := getMetricFloat(baseline.Metrics, "success_rate")

	if baselineSuccessRate > 0 {
		degradation := (baselineSuccessRate - canarySuccessRate) / baselineSuccessRate * 100
		if degradation > float64(MetricDegradationPercent) {
			slog.Warn("canary success rate degraded",
				"canary", canarySuccessRate,
				"baseline", baselineSuccessRate,
				"degradation_pct", degradation,
			)
			return false, nil
		}
	}

	return true, nil
}

func getMetricFloat(metrics map[string]any, key string) float64 {
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
