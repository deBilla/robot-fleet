package store

import (
	"context"
	"fmt"
	"time"
)

// ModelRecord represents a model in the model registry.
type ModelRecord struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	ArtifactURL string            `json:"artifact_url"`
	Status      string            `json:"status"` // staged, canary, deployed, archived
	Metrics     map[string]any    `json:"metrics"`
	CreatedAt   time.Time         `json:"created_at"`
	DeployedAt  *time.Time        `json:"deployed_at,omitempty"`
}

// RegisterModel inserts a new model into the registry.
func (s *PostgresStore) RegisterModel(ctx context.Context, m *ModelRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO model_registry (id, name, version, artifact_url, status, metrics, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, m.ID, m.Name, m.Version, m.ArtifactURL, m.Status, m.Metrics, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("register model %s: %w", m.ID, err)
	}
	return nil
}

// GetModel retrieves a model by ID.
func (s *PostgresStore) GetModel(ctx context.Context, id string) (*ModelRecord, error) {
	m := &ModelRecord{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, version, artifact_url, status, metrics, created_at, deployed_at
		FROM model_registry WHERE id = $1
	`, id).Scan(&m.ID, &m.Name, &m.Version, &m.ArtifactURL, &m.Status, &m.Metrics, &m.CreatedAt, &m.DeployedAt)
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", id, err)
	}
	return m, nil
}

// ListModels returns all models, optionally filtered by status.
func (s *PostgresStore) ListModels(ctx context.Context, status string) ([]*ModelRecord, error) {
	query := `SELECT id, name, version, artifact_url, status, metrics, created_at, deployed_at FROM model_registry`
	args := []any{}
	if status != "" {
		query += ` WHERE status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()

	var models []*ModelRecord
	for rows.Next() {
		m := &ModelRecord{}
		if err := rows.Scan(&m.ID, &m.Name, &m.Version, &m.ArtifactURL, &m.Status, &m.Metrics, &m.CreatedAt, &m.DeployedAt); err != nil {
			return nil, fmt.Errorf("scan model row: %w", err)
		}
		models = append(models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("model rows iteration: %w", err)
	}
	return models, nil
}

// UpdateModelStatus transitions a model to a new status.
func (s *PostgresStore) UpdateModelStatus(ctx context.Context, id, status string) error {
	query := `UPDATE model_registry SET status = $1 WHERE id = $2`
	args := []any{status, id}

	if status == "deployed" {
		query = `UPDATE model_registry SET status = $1, deployed_at = NOW() WHERE id = $2`
	}

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update model %s status to %s: %w", id, status, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("model %s not found", id)
	}
	return nil
}
