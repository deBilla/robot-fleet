package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore handles all PostgreSQL operations.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 20
	config.MinConns = 5

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}

	slog.Info("connected to postgresql")
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}

// Pool returns the underlying connection pool for middleware that needs direct DB access.
func (s *PostgresStore) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *PostgresStore) UpsertRobot(ctx context.Context, r *RobotRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO robots (id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id, inference_model_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			COALESCE(NULLIF($12, ''), (SELECT id FROM model_registry WHERE status = 'deployed' ORDER BY deployed_at DESC NULLS LAST LIMIT 1)))
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			pos_x = EXCLUDED.pos_x,
			pos_y = EXCLUDED.pos_y,
			pos_z = EXCLUDED.pos_z,
			battery_level = EXCLUDED.battery_level,
			last_seen = EXCLUDED.last_seen,
			inference_model_id = COALESCE(NULLIF(EXCLUDED.inference_model_id, ''), robots.inference_model_id)
	`, r.ID, r.Name, r.Model, r.Status, r.PosX, r.PosY, r.PosZ, r.BatteryLevel, r.LastSeen, r.RegisteredAt, r.TenantID, r.InferenceModelID)
	if err != nil {
		return fmt.Errorf("upsert robot %s: %w", r.ID, err)
	}
	return nil
}

func (s *PostgresStore) GetRobot(ctx context.Context, id string) (*RobotRecord, error) {
	r := &RobotRecord{}
	var inferenceModelID *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id, inference_model_id
		FROM robots WHERE id = $1
	`, id).Scan(&r.ID, &r.Name, &r.Model, &r.Status, &r.PosX, &r.PosY, &r.PosZ, &r.BatteryLevel, &r.LastSeen, &r.RegisteredAt, &r.TenantID, &inferenceModelID)
	if err != nil {
		return nil, fmt.Errorf("get robot %s: %w", id, err)
	}
	if inferenceModelID != nil {
		r.InferenceModelID = *inferenceModelID
	}
	return r, nil
}

func (s *PostgresStore) ListAllActiveRobots(ctx context.Context, since time.Time, limit int) ([]*RobotRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id, inference_model_id
		FROM robots WHERE last_seen >= $1
		ORDER BY id
		LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list all active robots: %w", err)
	}
	defer rows.Close()

	var robots []*RobotRecord
	for rows.Next() {
		r := &RobotRecord{}
		var inferenceModelID *string
		if err := rows.Scan(&r.ID, &r.Name, &r.Model, &r.Status, &r.PosX, &r.PosY, &r.PosZ, &r.BatteryLevel, &r.LastSeen, &r.RegisteredAt, &r.TenantID, &inferenceModelID); err != nil {
			return nil, fmt.Errorf("scan active robot row: %w", err)
		}
		if inferenceModelID != nil {
			r.InferenceModelID = *inferenceModelID
		}
		robots = append(robots, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration for active robots: %w", err)
	}
	return robots, nil
}

func (s *PostgresStore) ListRobots(ctx context.Context, tenantID string, limit, offset int) ([]*RobotRecord, int, error) {
	var total int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM robots WHERE tenant_id = $1`, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count robots for tenant %s: %w", tenantID, err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id, inference_model_id
		FROM robots WHERE tenant_id = $1
		ORDER BY id
		LIMIT $2 OFFSET $3
	`, tenantID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list robots for tenant %s: %w", tenantID, err)
	}
	defer rows.Close()

	var robots []*RobotRecord
	for rows.Next() {
		r := &RobotRecord{}
		var inferenceModelID *string
		if err := rows.Scan(&r.ID, &r.Name, &r.Model, &r.Status, &r.PosX, &r.PosY, &r.PosZ, &r.BatteryLevel, &r.LastSeen, &r.RegisteredAt, &r.TenantID, &inferenceModelID); err != nil {
			return nil, 0, fmt.Errorf("scan robot row: %w", err)
		}
		if inferenceModelID != nil {
			r.InferenceModelID = *inferenceModelID
		}
		robots = append(robots, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows iteration for tenant %s: %w", tenantID, err)
	}
	return robots, total, nil
}

// UpdateRobotInferenceModel assigns an inference model to a robot.
func (s *PostgresStore) UpdateRobotInferenceModel(ctx context.Context, robotID, modelID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE robots SET inference_model_id = $2 WHERE id = $1`, robotID, modelID)
	if err != nil {
		return fmt.Errorf("update inference model for robot %s: %w", robotID, err)
	}
	return nil
}

// ListRobotsByInferenceModel returns all robots assigned to a specific model.
func (s *PostgresStore) ListRobotsByInferenceModel(ctx context.Context, modelID string) ([]*RobotRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id, inference_model_id
		FROM robots WHERE inference_model_id = $1
		ORDER BY id
	`, modelID)
	if err != nil {
		return nil, fmt.Errorf("list robots by inference model %s: %w", modelID, err)
	}
	defer rows.Close()

	var robots []*RobotRecord
	for rows.Next() {
		r := &RobotRecord{}
		var inferenceModelID *string
		if err := rows.Scan(&r.ID, &r.Name, &r.Model, &r.Status, &r.PosX, &r.PosY, &r.PosZ, &r.BatteryLevel, &r.LastSeen, &r.RegisteredAt, &r.TenantID, &inferenceModelID); err != nil {
			return nil, fmt.Errorf("scan robot: %w", err)
		}
		if inferenceModelID != nil {
			r.InferenceModelID = *inferenceModelID
		}
		robots = append(robots, r)
	}
	return robots, nil
}

// StoreTelemetryEvent saves a raw telemetry event for historical queries.
func (s *PostgresStore) StoreTelemetryEvent(ctx context.Context, robotID, eventType string, payload []byte, ts time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO telemetry_events (robot_id, event_type, payload, created_at)
		VALUES ($1, $2, $3, $4)
	`, robotID, eventType, payload, ts)
	if err != nil {
		return fmt.Errorf("store telemetry event for %s: %w", robotID, err)
	}
	return nil
}

// StoreAPIUsage records an API call for metering and billing.
func (s *PostgresStore) StoreAPIUsage(ctx context.Context, tenantID, endpoint, method string, statusCode int, latencyMs int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO api_usage (tenant_id, endpoint, method, status_code, latency_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
	`, tenantID, endpoint, method, statusCode, latencyMs)
	if err != nil {
		return fmt.Errorf("store api usage for %s: %w", tenantID, err)
	}
	return nil
}

// InsertCommandAudit appends an immutable audit entry for a command lifecycle event.
func (s *PostgresStore) InsertCommandAudit(ctx context.Context, entry *CommandAuditEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO command_audit (command_id, robot_id, tenant_id, command_type, payload, status, instruction, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
	`, entry.CommandID, entry.RobotID, entry.TenantID, entry.CommandType, entry.Payload, entry.Status, entry.Instruction, entry.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("insert command audit for %s: %w", entry.CommandID, err)
	}
	return nil
}

// UpdateCommandAuditStatus updates the status of a command audit entry.
func (s *PostgresStore) UpdateCommandAuditStatus(ctx context.Context, commandID, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE command_audit SET status = $1 WHERE command_id = $2`, status, commandID)
	if err != nil {
		return fmt.Errorf("update command audit status for %s: %w", commandID, err)
	}
	return nil
}

// ListCommandAudit retrieves command audit entries for a robot, ordered by most recent.
func (s *PostgresStore) ListCommandAudit(ctx context.Context, robotID, tenantID string, limit int) ([]*CommandAuditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, command_id, robot_id, tenant_id, command_type, payload, status, instruction, idempotency_key, created_at
		FROM command_audit
		WHERE robot_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, robotID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list command audit for robot %s: %w", robotID, err)
	}
	defer rows.Close()

	var entries []*CommandAuditEntry
	for rows.Next() {
		e := &CommandAuditEntry{}
		if err := rows.Scan(&e.ID, &e.CommandID, &e.RobotID, &e.TenantID, &e.CommandType, &e.Payload, &e.Status, &e.Instruction, &e.IdempotencyKey, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan command audit row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
