package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CreateAgent inserts a new agent record.
func (s *PostgresStore) CreateAgent(ctx context.Context, a *AgentRecord) error {
	skills, _ := json.Marshal(a.MotorSkills)
	deps, _ := json.Marshal(a.ModelDeps)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, tenant_id, name, version, runtime, entrypoint, artifact_url,
			safety_envelope, motor_skills, model_deps, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, a.ID, a.TenantID, a.Name, a.Version, a.Runtime, a.Entrypoint, a.ArtifactURL,
		a.SafetyEnvelope, skills, deps, a.Status, a.CreatedBy, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create agent %s: %w", a.ID, err)
	}
	return nil
}

// GetAgent retrieves an agent by ID.
func (s *PostgresStore) GetAgent(ctx context.Context, id string) (*AgentRecord, error) {
	a := &AgentRecord{}
	var skills, deps []byte

	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, version, runtime, entrypoint, artifact_url,
			safety_envelope, motor_skills, model_deps, status, created_by, created_at, updated_at
		FROM agents WHERE id = $1
	`, id).Scan(&a.ID, &a.TenantID, &a.Name, &a.Version, &a.Runtime, &a.Entrypoint,
		&a.ArtifactURL, &a.SafetyEnvelope, &skills, &deps, &a.Status, &a.CreatedBy,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	_ = json.Unmarshal(skills, &a.MotorSkills)
	_ = json.Unmarshal(deps, &a.ModelDeps)
	return a, nil
}

// GetAgentByNameVersion retrieves an agent by tenant, name, and version.
func (s *PostgresStore) GetAgentByNameVersion(ctx context.Context, tenantID, name, version string) (*AgentRecord, error) {
	a := &AgentRecord{}
	var skills, deps []byte

	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, version, runtime, entrypoint, artifact_url,
			safety_envelope, motor_skills, model_deps, status, created_by, created_at, updated_at
		FROM agents WHERE tenant_id = $1 AND name = $2 AND version = $3
	`, tenantID, name, version).Scan(&a.ID, &a.TenantID, &a.Name, &a.Version, &a.Runtime,
		&a.Entrypoint, &a.ArtifactURL, &a.SafetyEnvelope, &skills, &deps, &a.Status,
		&a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get agent %s/%s for tenant %s: %w", name, version, tenantID, err)
	}
	_ = json.Unmarshal(skills, &a.MotorSkills)
	_ = json.Unmarshal(deps, &a.ModelDeps)
	return a, nil
}

// ListAgents returns agents for a tenant, optionally filtered by status.
func (s *PostgresStore) ListAgents(ctx context.Context, tenantID, status string, limit, offset int) ([]*AgentRecord, int, error) {
	// Count query
	countQuery := `SELECT COUNT(*) FROM agents WHERE tenant_id = $1`
	countArgs := []any{tenantID}
	if status != "" {
		countQuery += ` AND status = $2`
		countArgs = append(countArgs, status)
	}

	var total int
	if err := s.pool.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count agents: %w", err)
	}

	// Data query
	query := `SELECT id, tenant_id, name, version, runtime, entrypoint, artifact_url,
		safety_envelope, motor_skills, model_deps, status, created_by, created_at, updated_at
		FROM agents WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(` AND status = $%d`, argIdx)
		args = append(args, status)
		argIdx++
	}
	query += ` ORDER BY created_at DESC`
	query += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []*AgentRecord
	for rows.Next() {
		a := &AgentRecord{}
		var skills, deps []byte
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &a.Version, &a.Runtime, &a.Entrypoint,
			&a.ArtifactURL, &a.SafetyEnvelope, &skills, &deps, &a.Status, &a.CreatedBy,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan agent row: %w", err)
		}
		_ = json.Unmarshal(skills, &a.MotorSkills)
		_ = json.Unmarshal(deps, &a.ModelDeps)
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("agent rows iteration: %w", err)
	}
	return agents, total, nil
}

// UpdateAgentStatus transitions an agent to a new status.
func (s *PostgresStore) UpdateAgentStatus(ctx context.Context, id, status string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE agents SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update agent %s status: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("agent %s not found", id)
	}
	return nil
}

// UpdateAgentArtifact sets the S3 artifact URL after upload.
func (s *PostgresStore) UpdateAgentArtifact(ctx context.Context, id, artifactURL string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE agents SET artifact_url = $1, updated_at = $2 WHERE id = $3`,
		artifactURL, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update agent %s artifact: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("agent %s not found", id)
	}
	return nil
}

// CreateDeployment inserts a new deployment record.
func (s *PostgresStore) CreateDeployment(ctx context.Context, d *DeploymentRecord) error {
	fleet, _ := json.Marshal(d.TargetFleet)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO deployments (id, agent_id, tenant_id, status, strategy, canary_percentage,
			target_fleet, safety_envelope_override, initiated_by, initiated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, d.ID, d.AgentID, d.TenantID, d.Status, d.Strategy, d.CanaryPercentage,
		fleet, d.SafetyEnvelopeOverride, d.InitiatedBy, d.InitiatedAt)
	if err != nil {
		return fmt.Errorf("create deployment %s: %w", d.ID, err)
	}
	return nil
}

// GetDeployment retrieves a deployment by ID.
func (s *PostgresStore) GetDeployment(ctx context.Context, id string) (*DeploymentRecord, error) {
	d := &DeploymentRecord{}
	var fleet []byte
	var envelopeOverride, validationReport []byte

	err := s.pool.QueryRow(ctx, `
		SELECT id, agent_id, tenant_id, status, strategy, canary_percentage,
			target_fleet, COALESCE(safety_envelope_override::TEXT, '{}')::JSONB,
			COALESCE(validation_report::TEXT, '{}')::JSONB,
			COALESCE(rollback_reason, ''), initiated_by, initiated_at, completed_at
		FROM deployments WHERE id = $1
	`, id).Scan(&d.ID, &d.AgentID, &d.TenantID, &d.Status, &d.Strategy,
		&d.CanaryPercentage, &fleet, &envelopeOverride, &validationReport,
		&d.RollbackReason, &d.InitiatedBy, &d.InitiatedAt, &d.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", id, err)
	}
	_ = json.Unmarshal(fleet, &d.TargetFleet)                       // best-effort: already fetched from DB
	_ = json.Unmarshal(envelopeOverride, &d.SafetyEnvelopeOverride) // best-effort
	_ = json.Unmarshal(validationReport, &d.ValidationReport)       // best-effort
	return d, nil
}

// ListDeployments returns deployments for an agent.
func (s *PostgresStore) ListDeployments(ctx context.Context, agentID string, limit int) ([]*DeploymentRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_id, tenant_id, status, strategy, canary_percentage,
			target_fleet, COALESCE(safety_envelope_override::TEXT, '{}')::JSONB,
			COALESCE(validation_report::TEXT, '{}')::JSONB,
			COALESCE(rollback_reason, ''), initiated_by, initiated_at, completed_at
		FROM deployments WHERE agent_id = $1
		ORDER BY initiated_at DESC LIMIT $2
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployments for agent %s: %w", agentID, err)
	}
	defer rows.Close()

	var deployments []*DeploymentRecord
	for rows.Next() {
		d := &DeploymentRecord{}
		var fleet, envelopeOverride, validationReport []byte
		if err := rows.Scan(&d.ID, &d.AgentID, &d.TenantID, &d.Status, &d.Strategy,
			&d.CanaryPercentage, &fleet, &envelopeOverride, &validationReport,
			&d.RollbackReason, &d.InitiatedBy, &d.InitiatedAt, &d.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan deployment row: %w", err)
		}
		_ = json.Unmarshal(fleet, &d.TargetFleet)                       // best-effort
		_ = json.Unmarshal(envelopeOverride, &d.SafetyEnvelopeOverride) // best-effort
		_ = json.Unmarshal(validationReport, &d.ValidationReport)       // best-effort
		deployments = append(deployments, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deployment rows iteration: %w", err)
	}
	return deployments, nil
}

// UpdateDeploymentStatus transitions a deployment to a new status.
func (s *PostgresStore) UpdateDeploymentStatus(ctx context.Context, id, status string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE deployments SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("update deployment %s status: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("deployment %s not found", id)
	}
	return nil
}

// UpdateDeploymentValidation stores the simulation validation report.
func (s *PostgresStore) UpdateDeploymentValidation(ctx context.Context, id string, report map[string]any) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE deployments SET validation_report = $1 WHERE id = $2`, report, id)
	if err != nil {
		return fmt.Errorf("update deployment %s validation: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("deployment %s not found", id)
	}
	return nil
}

// SetDeploymentCompleted marks a deployment as complete or rolled back.
func (s *PostgresStore) SetDeploymentCompleted(ctx context.Context, id string, rollbackReason string) error {
	status := "complete"
	if rollbackReason != "" {
		status = "rolled_back"
	}
	result, err := s.pool.Exec(ctx,
		`UPDATE deployments SET status = $1, completed_at = $2, rollback_reason = $3 WHERE id = $4`,
		status, time.Now().UTC(), rollbackReason, id)
	if err != nil {
		return fmt.Errorf("complete deployment %s: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("deployment %s not found", id)
	}
	return nil
}

// AppendDeploymentEvent inserts a deployment lifecycle event.
func (s *PostgresStore) AppendDeploymentEvent(ctx context.Context, deploymentID, eventType string, data map[string]any) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO deployment_events (deployment_id, event_type, event_data)
		VALUES ($1, $2, $3)
	`, deploymentID, eventType, data)
	if err != nil {
		return fmt.Errorf("append deployment event: %w", err)
	}
	return nil
}

// ListDeploymentEvents returns events for a deployment in chronological order.
func (s *PostgresStore) ListDeploymentEvents(ctx context.Context, deploymentID string) ([]*DeploymentEventRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, event_type, event_data, created_at
		FROM deployment_events WHERE deployment_id = $1
		ORDER BY created_at ASC
	`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list deployment events: %w", err)
	}
	defer rows.Close()

	var events []*DeploymentEventRecord
	for rows.Next() {
		e := &DeploymentEventRecord{}
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.EventType, &e.EventData, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan deployment event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
