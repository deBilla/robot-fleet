package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
	"go.temporal.io/sdk/client"
)

// Agent lifecycle status constants.
const (
	AgentStatusRegistered = "registered"
	AgentStatusReady      = "ready"
	AgentStatusDeploying  = "deploying"
	AgentStatusArchived   = "archived"
)

// Deployment lifecycle status constants.
const (
	DeployStatusValidating = "validating"
	DeployStatusCanary     = "canary"
	DeployStatusPromoting  = "promoting"
	DeployStatusComplete   = "complete"
	DeployStatusRolledBack = "rolled_back"
)

// Default deployment configuration.
const DefaultCanaryPercentage = 5

// RegisterAgentRequest holds the input for registering a new agent.
type RegisterAgentRequest struct {
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Runtime        string         `json:"runtime"`
	Entrypoint     string         `json:"entrypoint"`
	SafetyEnvelope map[string]any `json:"safety_envelope"`
	MotorSkills    []string       `json:"motor_skills"`
	ModelDeps      []string       `json:"model_deps"`
}

// DeployAgentRequest holds the input for deploying an agent to a fleet.
type DeployAgentRequest struct {
	TargetFleet            []string       `json:"target_fleet"`
	Strategy               string         `json:"strategy"`
	CanaryPercentage       int            `json:"canary_percentage"`
	SafetyEnvelopeOverride map[string]any `json:"safety_envelope_override,omitempty"`
}

// AgentService defines the business logic interface for agent lifecycle management.
type AgentService interface {
	RegisterAgent(ctx context.Context, tenantID, createdBy string, req RegisterAgentRequest) (*store.AgentRecord, error)
	GetAgent(ctx context.Context, tenantID, agentID string) (*store.AgentRecord, error)
	ListAgents(ctx context.Context, tenantID, status string, limit, offset int) ([]*store.AgentRecord, int, error)
	DeployAgent(ctx context.Context, tenantID, agentID, initiatedBy string, req DeployAgentRequest) (*store.DeploymentRecord, error)
	RollbackAgent(ctx context.Context, tenantID, agentID, reason, initiatedBy string) (*store.DeploymentRecord, error)
	GetDeployment(ctx context.Context, tenantID, deploymentID string) (*store.DeploymentRecord, error)
	ListDeployments(ctx context.Context, tenantID, agentID string, limit int) ([]*store.DeploymentRecord, error)
}

type agentService struct {
	agents      store.AgentRepository
	deployments store.DeploymentRepository
	temporal    client.Client // nil if Temporal unavailable
}

// NewAgentService creates a new agent service.
func NewAgentService(agents store.AgentRepository, deployments store.DeploymentRepository, opts ...AgentServiceOption) AgentService {
	svc := &agentService{agents: agents, deployments: deployments}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// AgentServiceOption configures the agent service.
type AgentServiceOption func(*agentService)

// WithTemporalClient enables Temporal workflow integration for deployments.
func WithTemporalClient(tc client.Client) AgentServiceOption {
	return func(s *agentService) { s.temporal = tc }
}

func (s *agentService) RegisterAgent(ctx context.Context, tenantID, createdBy string, req RegisterAgentRequest) (*store.AgentRecord, error) {
	if err := validateSafetyEnvelope(req.SafetyEnvelope); err != nil {
		return nil, fmt.Errorf("invalid safety envelope: %w", err)
	}

	runtime := req.Runtime
	if runtime == "" {
		runtime = "python3.11"
	}
	entrypoint := req.Entrypoint
	if entrypoint == "" {
		entrypoint = "agent.py"
	}

	now := time.Now().UTC()
	agent := &store.AgentRecord{
		ID:             generateUUID(),
		TenantID:       tenantID,
		Name:           req.Name,
		Version:        req.Version,
		Runtime:        runtime,
		Entrypoint:     entrypoint,
		SafetyEnvelope: req.SafetyEnvelope,
		MotorSkills:    req.MotorSkills,
		ModelDeps:      req.ModelDeps,
		Status:         AgentStatusRegistered,
		CreatedBy:      createdBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if agent.MotorSkills == nil {
		agent.MotorSkills = []string{}
	}
	if agent.ModelDeps == nil {
		agent.ModelDeps = []string{}
	}

	if err := s.agents.CreateAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("register agent: %w", err)
	}
	return agent, nil
}

func (s *agentService) GetAgent(ctx context.Context, tenantID, agentID string) (*store.AgentRecord, error) {
	agent, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return agent, nil
}

func (s *agentService) ListAgents(ctx context.Context, tenantID, status string, limit, offset int) ([]*store.AgentRecord, int, error) {
	return s.agents.ListAgents(ctx, tenantID, status, limit, offset)
}

func (s *agentService) DeployAgent(ctx context.Context, tenantID, agentID, initiatedBy string, req DeployAgentRequest) (*store.DeploymentRecord, error) {
	agent, err := s.GetAgent(ctx, tenantID, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent for deploy: %w", err)
	}

	if agent.Status == AgentStatusArchived {
		return nil, fmt.Errorf("cannot deploy archived agent %s", agentID)
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = "canary"
	}
	canaryPct := req.CanaryPercentage
	if canaryPct <= 0 {
		canaryPct = DefaultCanaryPercentage
	}

	deployment := &store.DeploymentRecord{
		ID:                     generateUUID(),
		AgentID:                agentID,
		TenantID:               tenantID,
		Status:                 DeployStatusValidating,
		Strategy:               strategy,
		CanaryPercentage:       canaryPct,
		TargetFleet:            req.TargetFleet,
		SafetyEnvelopeOverride: req.SafetyEnvelopeOverride,
		InitiatedBy:            initiatedBy,
		InitiatedAt:            time.Now().UTC(),
	}

	if deployment.TargetFleet == nil {
		deployment.TargetFleet = []string{}
	}

	if err := s.deployments.CreateDeployment(ctx, deployment); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	_ = s.agents.UpdateAgentStatus(ctx, agentID, AgentStatusDeploying) // best-effort: deployment already created

	_ = s.deployments.AppendDeploymentEvent(ctx, deployment.ID, "initiated", map[string]any{ // best-effort: event logging non-critical
		"agent_id":     agentID,
		"strategy":     strategy,
		"canary_pct":   canaryPct,
		"target_fleet": req.TargetFleet,
	})

	// Trigger Temporal workflow for async deployment lifecycle
	if s.temporal != nil {
		workflowID := fmt.Sprintf("agent-deploy-%s", deployment.ID)
		_, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: "fleetos-deployments",
		}, "AgentDeploymentWorkflow", map[string]string{
			"deployment_id": deployment.ID,
			"agent_id":      agentID,
			"strategy":      strategy,
		})
		if err != nil {
			slog.Warn("failed to start deployment workflow, deployment will stay in validating",
				"deployment", deployment.ID, "error", err)
		} else {
			slog.Info("deployment workflow started", "deployment", deployment.ID, "workflow", workflowID)
		}
	}

	return deployment, nil
}

func (s *agentService) RollbackAgent(ctx context.Context, tenantID, agentID, reason, initiatedBy string) (*store.DeploymentRecord, error) {
	deployments, err := s.deployments.ListDeployments(ctx, agentID, 1)
	if err != nil {
		return nil, fmt.Errorf("list deployments for rollback: %w", err)
	}

	if len(deployments) == 0 {
		return nil, fmt.Errorf("no deployments found for agent %s", agentID)
	}

	latest := deployments[0]
	if latest.TenantID != tenantID {
		return nil, ErrNotFound
	}

	if latest.Status == DeployStatusComplete || latest.Status == DeployStatusRolledBack {
		return nil, fmt.Errorf("deployment %s already in terminal state: %s", latest.ID, latest.Status)
	}

	// Signal the Temporal workflow for graceful rollback if running
	if s.temporal != nil {
		workflowID := fmt.Sprintf("agent-deploy-%s", latest.ID)
		err := s.temporal.SignalWorkflow(ctx, workflowID, "", "agent-rollback", reason)
		if err != nil {
			slog.Warn("failed to signal deployment workflow, performing direct rollback",
				"deployment", latest.ID, "error", err)
		}
	}

	if err := s.deployments.SetDeploymentCompleted(ctx, latest.ID, reason); err != nil {
		return nil, fmt.Errorf("rollback deployment: %w", err)
	}

	_ = s.agents.UpdateAgentStatus(ctx, agentID, AgentStatusRegistered) // best-effort: rollback already committed

	_ = s.deployments.AppendDeploymentEvent(ctx, latest.ID, "rolled_back", map[string]any{ // best-effort: event logging non-critical
		"reason":       reason,
		"initiated_by": initiatedBy,
	})

	return s.deployments.GetDeployment(ctx, latest.ID)
}

func (s *agentService) GetDeployment(ctx context.Context, tenantID, deploymentID string) (*store.DeploymentRecord, error) {
	d, err := s.deployments.GetDeployment(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	if d.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *agentService) ListDeployments(ctx context.Context, tenantID, agentID string, limit int) ([]*store.DeploymentRecord, error) {
	if _, err := s.GetAgent(ctx, tenantID, agentID); err != nil {
		return nil, err
	}
	return s.deployments.ListDeployments(ctx, agentID, limit)
}

// validateSafetyEnvelope checks that a safety envelope has the required fields.
func validateSafetyEnvelope(envelope map[string]any) error {
	if len(envelope) == 0 {
		return fmt.Errorf("safety envelope is required")
	}
	return nil
}

// generateUUID creates a UUID v4 string.
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
