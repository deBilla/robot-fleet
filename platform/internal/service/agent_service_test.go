package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// --- Mock Agent Repository ---

type mockAgentRepo struct {
	agents map[string]*store.AgentRecord
	err    error
}

func newMockAgentRepo() *mockAgentRepo {
	return &mockAgentRepo{agents: make(map[string]*store.AgentRecord)}
}

func (m *mockAgentRepo) CreateAgent(_ context.Context, a *store.AgentRecord) error {
	if m.err != nil {
		return m.err
	}
	// Check unique constraint
	for _, existing := range m.agents {
		if existing.TenantID == a.TenantID && existing.Name == a.Name && existing.Version == a.Version {
			return fmt.Errorf("agent %s/%s already exists for tenant %s", a.Name, a.Version, a.TenantID)
		}
	}
	m.agents[a.ID] = a
	return nil
}

func (m *mockAgentRepo) GetAgent(_ context.Context, id string) (*store.AgentRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	a, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", id)
	}
	return a, nil
}

func (m *mockAgentRepo) GetAgentByNameVersion(_ context.Context, tenantID, name, version string) (*store.AgentRecord, error) {
	for _, a := range m.agents {
		if a.TenantID == tenantID && a.Name == name && a.Version == version {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agent %s/%s not found", name, version)
}

func (m *mockAgentRepo) ListAgents(_ context.Context, tenantID, status string, limit, offset int) ([]*store.AgentRecord, int, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	var filtered []*store.AgentRecord
	for _, a := range m.agents {
		if a.TenantID != tenantID {
			continue
		}
		if status != "" && a.Status != status {
			continue
		}
		filtered = append(filtered, a)
	}
	total := len(filtered)
	end := offset + limit
	if end > total {
		end = total
	}
	if offset > total {
		return nil, total, nil
	}
	return filtered[offset:end], total, nil
}

func (m *mockAgentRepo) UpdateAgentStatus(_ context.Context, id, status string) error {
	a, ok := m.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}
	a.Status = status
	return nil
}

func (m *mockAgentRepo) UpdateAgentArtifact(_ context.Context, id, url string) error {
	a, ok := m.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}
	a.ArtifactURL = url
	return nil
}

// --- Mock Deployment Repository ---

type mockDeployRepo struct {
	deployments map[string]*store.DeploymentRecord
	events      []*store.DeploymentEventRecord
	err         error
}

func newMockDeployRepo() *mockDeployRepo {
	return &mockDeployRepo{
		deployments: make(map[string]*store.DeploymentRecord),
	}
}

func (m *mockDeployRepo) CreateDeployment(_ context.Context, d *store.DeploymentRecord) error {
	if m.err != nil {
		return m.err
	}
	m.deployments[d.ID] = d
	return nil
}

func (m *mockDeployRepo) GetDeployment(_ context.Context, id string) (*store.DeploymentRecord, error) {
	d, ok := m.deployments[id]
	if !ok {
		return nil, fmt.Errorf("deployment %s not found", id)
	}
	return d, nil
}

func (m *mockDeployRepo) ListDeployments(_ context.Context, agentID string, limit int) ([]*store.DeploymentRecord, error) {
	var result []*store.DeploymentRecord
	for _, d := range m.deployments {
		if d.AgentID == agentID {
			result = append(result, d)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockDeployRepo) UpdateDeploymentStatus(_ context.Context, id, status string) error {
	d, ok := m.deployments[id]
	if !ok {
		return fmt.Errorf("deployment %s not found", id)
	}
	d.Status = status
	return nil
}

func (m *mockDeployRepo) UpdateDeploymentValidation(_ context.Context, id string, report map[string]any) error {
	d, ok := m.deployments[id]
	if !ok {
		return fmt.Errorf("deployment %s not found", id)
	}
	d.ValidationReport = report
	return nil
}

func (m *mockDeployRepo) SetDeploymentCompleted(_ context.Context, id string, rollbackReason string) error {
	d, ok := m.deployments[id]
	if !ok {
		return fmt.Errorf("deployment %s not found", id)
	}
	if rollbackReason != "" {
		d.Status = DeployStatusRolledBack
		d.RollbackReason = rollbackReason
	} else {
		d.Status = DeployStatusComplete
	}
	return nil
}

func (m *mockDeployRepo) AppendDeploymentEvent(_ context.Context, deploymentID, eventType string, data map[string]any) error {
	m.events = append(m.events, &store.DeploymentEventRecord{
		DeploymentID: deploymentID,
		EventType:    eventType,
		EventData:    data,
	})
	return nil
}

func (m *mockDeployRepo) ListDeploymentEvents(_ context.Context, deploymentID string) ([]*store.DeploymentEventRecord, error) {
	var result []*store.DeploymentEventRecord
	for _, e := range m.events {
		if e.DeploymentID == deploymentID {
			result = append(result, e)
		}
	}
	return result, nil
}

// --- Tests ---

func TestRegisterAgent(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	tests := []struct {
		name    string
		req     RegisterAgentRequest
		wantErr bool
	}{
		{
			name: "valid agent",
			req: RegisterAgentRequest{
				Name:           "warehouse-picker",
				Version:        "1.0.0",
				SafetyEnvelope: map[string]any{"max_velocity": 1.5},
				MotorSkills:    []string{"bipedal-walk"},
			},
			wantErr: false,
		},
		{
			name: "missing safety envelope",
			req: RegisterAgentRequest{
				Name:    "bad-agent",
				Version: "1.0.0",
			},
			wantErr: true,
		},
		{
			name: "defaults runtime and entrypoint",
			req: RegisterAgentRequest{
				Name:           "default-agent",
				Version:        "1.0.0",
				SafetyEnvelope: map[string]any{"max_force": 50},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, err := svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if agent.ID == "" {
				t.Error("agent ID should not be empty")
			}
			if agent.TenantID != "tenant-1" {
				t.Errorf("expected tenant-1, got %s", agent.TenantID)
			}
			if agent.Status != AgentStatusRegistered {
				t.Errorf("expected registered, got %s", agent.Status)
			}
			if agent.Runtime == "" {
				t.Error("runtime should have a default")
			}
		})
	}
}

func TestGetAgent_TenantIsolation(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	agent, err := svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", RegisterAgentRequest{
		Name:           "my-agent",
		Version:        "1.0.0",
		SafetyEnvelope: map[string]any{"max_velocity": 1.5},
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Same tenant can access
	got, err := svc.GetAgent(context.Background(), "tenant-1", agent.ID)
	if err != nil {
		t.Fatalf("same tenant should access: %v", err)
	}
	if got.ID != agent.ID {
		t.Error("should return the same agent")
	}

	// Different tenant gets not found
	_, err = svc.GetAgent(context.Background(), "tenant-2", agent.ID)
	if err != ErrNotFound {
		t.Errorf("different tenant should get ErrNotFound, got: %v", err)
	}
}

func TestDeployAgent(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	agent, _ := svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", RegisterAgentRequest{
		Name:           "deploy-test",
		Version:        "1.0.0",
		SafetyEnvelope: map[string]any{"max_velocity": 1.5},
	})

	deployment, err := svc.DeployAgent(context.Background(), "tenant-1", agent.ID, "user@test.com", DeployAgentRequest{
		TargetFleet: []string{"robot-001", "robot-002"},
		Strategy:    "canary",
	})
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	if deployment.Status != DeployStatusValidating {
		t.Errorf("expected validating, got %s", deployment.Status)
	}
	if deployment.AgentID != agent.ID {
		t.Errorf("expected agent ID %s, got %s", agent.ID, deployment.AgentID)
	}
	if deployment.CanaryPercentage != 5 {
		t.Errorf("expected default canary 5%%, got %d", deployment.CanaryPercentage)
	}

	// Agent should be marked as deploying
	updated, _ := svc.GetAgent(context.Background(), "tenant-1", agent.ID)
	if updated.Status != AgentStatusDeploying {
		t.Errorf("agent should be deploying, got %s", updated.Status)
	}

	// Should have emitted an initiated event
	if len(deploys.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(deploys.events))
	}
	if deploys.events[0].EventType != "initiated" {
		t.Errorf("expected initiated event, got %s", deploys.events[0].EventType)
	}
}

func TestDeployAgent_ArchivedRejected(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	agent, _ := svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", RegisterAgentRequest{
		Name:           "archived-agent",
		Version:        "1.0.0",
		SafetyEnvelope: map[string]any{"max_velocity": 1.5},
	})

	// Archive the agent
	agents.agents[agent.ID].Status = AgentStatusArchived

	_, err := svc.DeployAgent(context.Background(), "tenant-1", agent.ID, "user@test.com", DeployAgentRequest{})
	if err == nil {
		t.Fatal("should reject deploying archived agent")
	}
}

func TestRollbackAgent(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	agent, _ := svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", RegisterAgentRequest{
		Name:           "rollback-test",
		Version:        "1.0.0",
		SafetyEnvelope: map[string]any{"max_velocity": 1.5},
	})

	// Deploy first
	svc.DeployAgent(context.Background(), "tenant-1", agent.ID, "user@test.com", DeployAgentRequest{
		TargetFleet: []string{"robot-001"},
	})

	// Rollback
	rolled, err := svc.RollbackAgent(context.Background(), "tenant-1", agent.ID, "safety violation detected", "ops@test.com")
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	if rolled.Status != DeployStatusRolledBack {
		t.Errorf("expected rolled_back, got %s", rolled.Status)
	}
	if rolled.RollbackReason != "safety violation detected" {
		t.Errorf("expected reason, got %s", rolled.RollbackReason)
	}

	// Agent should be back to registered
	updated, _ := svc.GetAgent(context.Background(), "tenant-1", agent.ID)
	if updated.Status != AgentStatusRegistered {
		t.Errorf("agent should be registered after rollback, got %s", updated.Status)
	}
}

func TestRollbackAgent_NoDeployment(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	agent, _ := svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", RegisterAgentRequest{
		Name:           "no-deploy",
		Version:        "1.0.0",
		SafetyEnvelope: map[string]any{"max_velocity": 1.5},
	})

	_, err := svc.RollbackAgent(context.Background(), "tenant-1", agent.ID, "reason", "user")
	if err == nil {
		t.Fatal("should fail when no deployments exist")
	}
}

func TestListAgents_Pagination(t *testing.T) {
	agents := newMockAgentRepo()
	deploys := newMockDeployRepo()
	svc := NewAgentService(agents, deploys)

	for i := range 5 {
		svc.RegisterAgent(context.Background(), "tenant-1", "user@test.com", RegisterAgentRequest{
			Name:           fmt.Sprintf("agent-%d", i),
			Version:        "1.0.0",
			SafetyEnvelope: map[string]any{"max_velocity": 1.5},
		})
	}

	list, total, err := svc.ListAgents(context.Background(), "tenant-1", "", 3, 0)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 agents, got %d", len(list))
	}
}
