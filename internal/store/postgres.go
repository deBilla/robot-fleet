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

// Robot represents a robot record in the database.
type RobotRecord struct {
	ID           string
	Name         string
	Model        string
	Status       string
	PosX         float64
	PosY         float64
	PosZ         float64
	BatteryLevel float64
	LastSeen     time.Time
	RegisteredAt time.Time
	TenantID     string
	Metadata     map[string]string
}

func (s *PostgresStore) UpsertRobot(ctx context.Context, r *RobotRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO robots (id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			pos_x = EXCLUDED.pos_x,
			pos_y = EXCLUDED.pos_y,
			pos_z = EXCLUDED.pos_z,
			battery_level = EXCLUDED.battery_level,
			last_seen = EXCLUDED.last_seen
	`, r.ID, r.Name, r.Model, r.Status, r.PosX, r.PosY, r.PosZ, r.BatteryLevel, r.LastSeen, r.RegisteredAt, r.TenantID)
	if err != nil {
		return fmt.Errorf("upsert robot %s: %w", r.ID, err)
	}
	return nil
}

func (s *PostgresStore) GetRobot(ctx context.Context, id string) (*RobotRecord, error) {
	r := &RobotRecord{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id
		FROM robots WHERE id = $1
	`, id).Scan(&r.ID, &r.Name, &r.Model, &r.Status, &r.PosX, &r.PosY, &r.PosZ, &r.BatteryLevel, &r.LastSeen, &r.RegisteredAt, &r.TenantID)
	if err != nil {
		return nil, fmt.Errorf("get robot %s: %w", id, err)
	}
	return r, nil
}

func (s *PostgresStore) ListRobots(ctx context.Context, tenantID string, limit, offset int) ([]*RobotRecord, int, error) {
	var total int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM robots WHERE tenant_id = $1`, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count robots for tenant %s: %w", tenantID, err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, name, model, status, pos_x, pos_y, pos_z, battery_level, last_seen, registered_at, tenant_id
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
		if err := rows.Scan(&r.ID, &r.Name, &r.Model, &r.Status, &r.PosX, &r.PosY, &r.PosZ, &r.BatteryLevel, &r.LastSeen, &r.RegisteredAt, &r.TenantID); err != nil {
			return nil, 0, fmt.Errorf("scan robot row: %w", err)
		}
		robots = append(robots, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows iteration for tenant %s: %w", tenantID, err)
	}
	return robots, total, nil
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
