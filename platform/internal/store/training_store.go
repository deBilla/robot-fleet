package store

import (
	"context"
	"fmt"
	"time"
)

// CreateTrainingJob inserts a new training job record.
func (s *PostgresStore) CreateTrainingJob(ctx context.Context, j *TrainingJobRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO training_jobs (id, tenant_id, agent_id, status, algorithm, environment,
			timesteps, device, config, metrics, initiated_by, created_at)
		VALUES ($1, $2, NULLIF($3, '')::UUID, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, j.ID, j.TenantID, j.AgentID, j.Status, j.Algorithm, j.Environment,
		j.Timesteps, j.Device, j.Config, j.Metrics, j.InitiatedBy, j.CreatedAt)
	if err != nil {
		return fmt.Errorf("create training job %s: %w", j.ID, err)
	}
	return nil
}

// GetTrainingJob retrieves a training job by ID.
func (s *PostgresStore) GetTrainingJob(ctx context.Context, id string) (*TrainingJobRecord, error) {
	j := &TrainingJobRecord{}
	var agentID *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, COALESCE(agent_id::TEXT, ''), status, algorithm, environment,
			timesteps, device, config, metrics, COALESCE(artifact_url, ''),
			COALESCE(error_message, ''), initiated_by, created_at, started_at, completed_at
		FROM training_jobs WHERE id = $1
	`, id).Scan(&j.ID, &j.TenantID, &agentID, &j.Status, &j.Algorithm, &j.Environment,
		&j.Timesteps, &j.Device, &j.Config, &j.Metrics, &j.ArtifactURL,
		&j.ErrorMessage, &j.InitiatedBy, &j.CreatedAt, &j.StartedAt, &j.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("get training job %s: %w", id, err)
	}
	if agentID != nil {
		j.AgentID = *agentID
	}
	return j, nil
}

// ListTrainingJobs returns training jobs for a tenant.
func (s *PostgresStore) ListTrainingJobs(ctx context.Context, tenantID, status string, limit int) ([]*TrainingJobRecord, error) {
	query := `SELECT id, tenant_id, COALESCE(agent_id::TEXT, ''), status, algorithm, environment,
		timesteps, device, config, metrics, COALESCE(artifact_url, ''),
		COALESCE(error_message, ''), initiated_by, created_at, started_at, completed_at
		FROM training_jobs WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if status != "" {
		query += fmt.Sprintf(` AND status = $%d`, argIdx)
		args = append(args, status)
		argIdx++
	}
	query += ` ORDER BY created_at DESC`
	query += fmt.Sprintf(` LIMIT $%d`, argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list training jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*TrainingJobRecord
	for rows.Next() {
		j := &TrainingJobRecord{}
		var agentID *string
		if err := rows.Scan(&j.ID, &j.TenantID, &agentID, &j.Status, &j.Algorithm, &j.Environment,
			&j.Timesteps, &j.Device, &j.Config, &j.Metrics, &j.ArtifactURL,
			&j.ErrorMessage, &j.InitiatedBy, &j.CreatedAt, &j.StartedAt, &j.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan training job: %w", err)
		}
		if agentID != nil {
			j.AgentID = *agentID
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("training job rows iteration: %w", err)
	}
	return jobs, nil
}

// UpdateTrainingJobStatus transitions a job status and sets started_at if moving to running.
func (s *PostgresStore) UpdateTrainingJobStatus(ctx context.Context, id, status string) error {
	var err error
	if status == "running" {
		_, err = s.pool.Exec(ctx, `UPDATE training_jobs SET status = $1, started_at = $3 WHERE id = $2`, status, id, time.Now().UTC())
	} else {
		_, err = s.pool.Exec(ctx, `UPDATE training_jobs SET status = $1 WHERE id = $2`, status, id)
	}
	if err != nil {
		return fmt.Errorf("update training job %s status to %s: %w", id, status, err)
	}
	return nil
}

// UpdateTrainingJobMetrics updates the metrics JSONB field (e.g., from periodic callbacks).
func (s *PostgresStore) UpdateTrainingJobMetrics(ctx context.Context, id string, metrics map[string]any) error {
	_, err := s.pool.Exec(ctx, `UPDATE training_jobs SET metrics = $1 WHERE id = $2`, metrics, id)
	if err != nil {
		return fmt.Errorf("update training job %s metrics: %w", id, err)
	}
	return nil
}

// UpdateTrainingJobCompleted marks a training job as completed or failed.
func (s *PostgresStore) UpdateTrainingJobCompleted(ctx context.Context, id, status, artifactURL, errorMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE training_jobs SET status = $1, artifact_url = $2, error_message = $3, completed_at = $4
		WHERE id = $5
	`, status, artifactURL, errorMsg, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("complete training job %s: %w", id, err)
	}
	return nil
}

// CreateTrainingEval inserts a new evaluation record.
func (s *PostgresStore) CreateTrainingEval(ctx context.Context, e *TrainingEvalRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO training_evaluations (id, tenant_id, job_id, status, scenarios_total, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, e.ID, e.TenantID, e.JobID, e.Status, e.ScenariosTotal, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("create training eval %s: %w", e.ID, err)
	}
	return nil
}

// GetTrainingEval retrieves an evaluation by ID.
func (s *PostgresStore) GetTrainingEval(ctx context.Context, id string) (*TrainingEvalRecord, error) {
	e := &TrainingEvalRecord{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, job_id, status, scenarios_total, scenarios_passed, pass_rate,
			metrics, results, COALESCE(error_message, ''), created_at, started_at, completed_at
		FROM training_evaluations WHERE id = $1
	`, id).Scan(&e.ID, &e.TenantID, &e.JobID, &e.Status, &e.ScenariosTotal,
		&e.ScenariosPassed, &e.PassRate, &e.Metrics, &e.Results,
		&e.ErrorMessage, &e.CreatedAt, &e.StartedAt, &e.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("get training eval %s: %w", id, err)
	}
	return e, nil
}

// ListTrainingEvals returns evaluations for a tenant, optionally filtered by job.
func (s *PostgresStore) ListTrainingEvals(ctx context.Context, tenantID string, jobID string, limit int) ([]*TrainingEvalRecord, error) {
	query := `SELECT id, tenant_id, job_id, status, scenarios_total, scenarios_passed, pass_rate,
		metrics, results, COALESCE(error_message, ''), created_at, started_at, completed_at
		FROM training_evaluations WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if jobID != "" {
		query += fmt.Sprintf(` AND job_id = $%d`, argIdx)
		args = append(args, jobID)
		argIdx++
	}
	query += ` ORDER BY created_at DESC`
	query += fmt.Sprintf(` LIMIT $%d`, argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list training evals: %w", err)
	}
	defer rows.Close()

	var evals []*TrainingEvalRecord
	for rows.Next() {
		e := &TrainingEvalRecord{}
		if err := rows.Scan(&e.ID, &e.TenantID, &e.JobID, &e.Status, &e.ScenariosTotal,
			&e.ScenariosPassed, &e.PassRate, &e.Metrics, &e.Results,
			&e.ErrorMessage, &e.CreatedAt, &e.StartedAt, &e.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan training eval: %w", err)
		}
		evals = append(evals, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("training eval rows iteration: %w", err)
	}
	return evals, nil
}

// UpdateTrainingEvalCompleted marks an evaluation as completed.
func (s *PostgresStore) UpdateTrainingEvalCompleted(ctx context.Context, id string, passed, total int, passRate float64, results map[string]any, errorMsg string) error {
	status := "completed"
	if errorMsg != "" {
		status = "failed"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE training_evaluations SET status = $1, scenarios_passed = $2, scenarios_total = $3,
			pass_rate = $4, results = $5, error_message = $6, completed_at = $7
		WHERE id = $8
	`, status, passed, total, passRate, results, errorMsg, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("complete training eval %s: %w", id, err)
	}
	return nil
}
