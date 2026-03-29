package store

import (
	"context"
	"fmt"
)

// CreateSafetyIncident inserts a new safety incident.
func (s *PostgresStore) CreateSafetyIncident(ctx context.Context, i *SafetyIncidentRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO safety_incidents (id, robot_id, agent_id, deployment_id, site_id,
			incident_type, severity, details, telemetry_snapshot, created_at)
		VALUES ($1, $2, NULLIF($3, '')::UUID, NULLIF($4, '')::UUID, $5, $6, $7, $8, $9, $10)
	`, i.ID, i.RobotID, i.AgentID, i.DeploymentID, i.SiteID,
		i.IncidentType, i.Severity, i.Details, i.TelemetrySnapshot, i.CreatedAt)
	if err != nil {
		return fmt.Errorf("create safety incident: %w", err)
	}
	return nil
}

// ListSafetyIncidents returns incidents with optional filters.
func (s *PostgresStore) ListSafetyIncidents(ctx context.Context, severity, robotID string, limit int) ([]*SafetyIncidentRecord, error) {
	query := `SELECT id, robot_id, COALESCE(agent_id::TEXT, ''), COALESCE(deployment_id::TEXT, ''),
		site_id, incident_type, severity, details, telemetry_snapshot, resolved_at, COALESCE(resolution, ''), created_at
		FROM safety_incidents WHERE 1=1`
	args := []any{}
	argIdx := 1

	if severity != "" {
		query += fmt.Sprintf(` AND severity = $%d`, argIdx)
		args = append(args, severity)
		argIdx++
	}
	if robotID != "" {
		query += fmt.Sprintf(` AND robot_id = $%d`, argIdx)
		args = append(args, robotID)
		argIdx++
	}
	query += ` ORDER BY created_at DESC`
	query += fmt.Sprintf(` LIMIT $%d`, argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list safety incidents: %w", err)
	}
	defer rows.Close()

	var incidents []*SafetyIncidentRecord
	for rows.Next() {
		i := &SafetyIncidentRecord{}
		if err := rows.Scan(&i.ID, &i.RobotID, &i.AgentID, &i.DeploymentID,
			&i.SiteID, &i.IncidentType, &i.Severity, &i.Details,
			&i.TelemetrySnapshot, &i.ResolvedAt, &i.Resolution, &i.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan safety incident: %w", err)
		}
		incidents = append(incidents, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("safety incident rows iteration: %w", err)
	}
	return incidents, nil
}

// WriteAuditLog inserts an audit log entry.
func (s *PostgresStore) WriteAuditLog(ctx context.Context, tenantID, action, resourceType, resourceID string, details map[string]any) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (tenant_id, actor, action, resource_type, resource_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, tenantID, tenantID, action, resourceType, resourceID, details)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}
