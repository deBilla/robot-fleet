package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// AgentDeploymentActivities holds dependencies for agent deployment Temporal activities.
type AgentDeploymentActivities struct {
	Agents      store.AgentRepository
	Deployments store.DeploymentRepository
	Cache       store.CacheStore
}

// ValidateInput is the input for ValidateAgent.
type ValidateInput struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
}

// ValidateOutput is returned by ValidateAgent.
type ValidateOutput struct {
	Approved bool           `json:"approved"`
	Report   map[string]any `json:"report"`
}

// ValidateAgent runs pre-deployment validation (safety envelope check, future: Uranus sim).
// Transitions deployment: validating → canary (if approved) or → rolled_back (if rejected).
func (a *AgentDeploymentActivities) ValidateAgent(ctx context.Context, input ValidateInput) (*ValidateOutput, error) {
	agent, err := a.Agents.GetAgent(ctx, input.AgentID)
	if err != nil {
		return nil, fmt.Errorf("get agent %s: %w", input.AgentID, err)
	}

	report := map[string]any{
		"agent_id":       agent.ID,
		"agent_version":  agent.Version,
		"checks_passed":  0,
		"checks_total":   0,
		"safety_valid":   false,
	}

	// Check 1: safety envelope is non-empty
	checksTotal := 0
	checksPassed := 0

	checksTotal++
	if len(agent.SafetyEnvelope) > 0 {
		checksPassed++
		report["safety_valid"] = true
	}

	// Check 2: agent status is valid for deployment
	checksTotal++
	if agent.Status != "archived" {
		checksPassed++
	}

	report["checks_passed"] = checksPassed
	report["checks_total"] = checksTotal

	approved := checksPassed == checksTotal

	// Store validation report on the deployment
	_ = a.Deployments.UpdateDeploymentValidation(ctx, input.DeploymentID, report)

	a.emitEvent(ctx, input.DeploymentID, "validation_complete", map[string]any{
		"approved":      approved,
		"checks_passed": checksPassed,
		"checks_total":  checksTotal,
	})

	slog.Info("agent validation complete", "deployment", input.DeploymentID,
		"agent", input.AgentID, "approved", approved)

	return &ValidateOutput{Approved: approved, Report: report}, nil
}

// TransitionInput is the input for status transitions.
type TransitionInput struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
	NewStatus    string `json:"new_status"`
	Percent      int    `json:"percent"`
}

// TransitionDeployment updates the deployment status in the database and emits an event.
func (a *AgentDeploymentActivities) TransitionDeployment(ctx context.Context, input TransitionInput) error {
	if err := a.Deployments.UpdateDeploymentStatus(ctx, input.DeploymentID, input.NewStatus); err != nil {
		return fmt.Errorf("transition deployment %s to %s: %w", input.DeploymentID, input.NewStatus, err)
	}

	a.emitEvent(ctx, input.DeploymentID, "status_changed", map[string]any{
		"new_status": input.NewStatus,
		"percent":    input.Percent,
	})

	slog.Info("deployment transitioned", "deployment", input.DeploymentID,
		"status", input.NewStatus, "percent", input.Percent)
	return nil
}

// CheckMetricsInput is the input for CheckDeploymentMetrics.
type CheckMetricsInput struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
}

// CheckMetricsOutput is returned by CheckDeploymentMetrics.
type CheckMetricsOutput struct {
	Healthy              bool    `json:"healthy"`
	SafetyViolationRate  float64 `json:"safety_violation_rate"`
	TaskFailureRate      float64 `json:"task_failure_rate"`
	EstopCount           int     `json:"estop_count"`
}

// Rollback thresholds from Menlo spec.
const (
	MaxSafetyViolationRate = 0.001
	MaxTaskFailureRate     = 0.05
	MaxEstopCount          = 0
)

// CheckDeploymentMetrics evaluates canary health using Menlo-style criteria:
// safety_violation_rate, task_failure_rate, and estop_count.
func (a *AgentDeploymentActivities) CheckDeploymentMetrics(ctx context.Context, input CheckMetricsInput) (*CheckMetricsOutput, error) {
	// In production: query ClickHouse/Prometheus for real metrics from the canary fleet.
	// For now: assume healthy (metrics collection is wired separately via telemetry pipeline).
	output := &CheckMetricsOutput{
		Healthy:             true,
		SafetyViolationRate: 0.0,
		TaskFailureRate:     0.0,
		EstopCount:          0,
	}

	if output.SafetyViolationRate > MaxSafetyViolationRate {
		output.Healthy = false
	}
	if output.TaskFailureRate > MaxTaskFailureRate {
		output.Healthy = false
	}
	if output.EstopCount > MaxEstopCount {
		output.Healthy = false
	}

	a.emitEvent(ctx, input.DeploymentID, "metrics_check", map[string]any{
		"healthy":               output.Healthy,
		"safety_violation_rate": output.SafetyViolationRate,
		"task_failure_rate":     output.TaskFailureRate,
		"estop_count":           output.EstopCount,
	})

	return output, nil
}

// RollbackDeploymentInput is the input for RollbackDeployment.
type RollbackDeploymentInput struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
	Reason       string `json:"reason"`
}

// RollbackDeployment marks the deployment as rolled back and resets the agent status.
func (a *AgentDeploymentActivities) RollbackDeployment(ctx context.Context, input RollbackDeploymentInput) error {
	if err := a.Deployments.SetDeploymentCompleted(ctx, input.DeploymentID, input.Reason); err != nil {
		return fmt.Errorf("rollback deployment %s: %w", input.DeploymentID, err)
	}
	_ = a.Agents.UpdateAgentStatus(ctx, input.AgentID, "registered")

	a.emitEvent(ctx, input.DeploymentID, "rolled_back", map[string]any{
		"reason": input.Reason,
	})

	slog.Warn("deployment rolled back", "deployment", input.DeploymentID, "reason", input.Reason)
	return nil
}

// FinalizeDeploymentInput is the input for FinalizeAgentDeployment.
type FinalizeDeploymentInput struct {
	DeploymentID string `json:"deployment_id"`
	AgentID      string `json:"agent_id"`
}

// FinalizeAgentDeployment marks the deployment as complete and the agent as ready.
func (a *AgentDeploymentActivities) FinalizeAgentDeployment(ctx context.Context, input FinalizeDeploymentInput) error {
	if err := a.Deployments.SetDeploymentCompleted(ctx, input.DeploymentID, ""); err != nil {
		return fmt.Errorf("finalize deployment %s: %w", input.DeploymentID, err)
	}
	_ = a.Agents.UpdateAgentStatus(ctx, input.AgentID, "ready")

	a.emitEvent(ctx, input.DeploymentID, "complete", map[string]any{
		"agent_id": input.AgentID,
	})

	slog.Info("deployment finalized", "deployment", input.DeploymentID, "agent", input.AgentID)
	return nil
}

// emitEvent persists the event to DB and publishes to Redis for SSE streaming.
func (a *AgentDeploymentActivities) emitEvent(ctx context.Context, deploymentID, eventType string, data map[string]any) {
	_ = a.Deployments.AppendDeploymentEvent(ctx, deploymentID, eventType, data)

	// Publish to Redis pub/sub for SSE consumers
	if a.Cache != nil {
		channel := fmt.Sprintf("deployment:%s:events", deploymentID)
		payload, _ := json.Marshal(map[string]any{
			"deployment_id": deploymentID,
			"event_type":    eventType,
			"data":          data,
		})
		_ = a.Cache.PublishEvent(ctx, channel, payload)
	}
}
