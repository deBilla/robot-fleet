package store

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RobotRepository defines the contract for persistent robot storage.
type RobotRepository interface {
	UpsertRobot(ctx context.Context, r *RobotRecord) error
	GetRobot(ctx context.Context, id string) (*RobotRecord, error)
	ListRobots(ctx context.Context, tenantID string, limit, offset int) ([]*RobotRecord, int, error)
	StoreTelemetryEvent(ctx context.Context, robotID, eventType string, payload []byte, ts time.Time) error
	StoreAPIUsage(ctx context.Context, tenantID, endpoint, method string, statusCode int, latencyMs int64) error
	Close()
}

// CacheStore defines the contract for hot state caching and real-time operations.
type CacheStore interface {
	SetRobotState(ctx context.Context, state *RobotHotState) error
	GetRobotState(ctx context.Context, robotID string) (*RobotHotState, error)
	CheckRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time, error)
	IncrementUsageCounter(ctx context.Context, tenantID, metric string) (int64, error)
	GetUsageCounter(ctx context.Context, tenantID, metric, date string) (int64, error)
	PublishEvent(ctx context.Context, channel string, data []byte) error
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
	// Generic cache operations for arbitrary JSON data
	SetCacheJSON(ctx context.Context, key string, data []byte, ttl time.Duration) error
	GetCacheJSON(ctx context.Context, key string) ([]byte, error)
	Close()
}

// ModelRepository defines the contract for model registry operations.
type ModelRepository interface {
	RegisterModel(ctx context.Context, m *ModelRecord) error
	GetModel(ctx context.Context, id string) (*ModelRecord, error)
	ListModels(ctx context.Context, status string) ([]*ModelRecord, error)
	UpdateModelStatus(ctx context.Context, id, status string) error
}

// Compile-time interface compliance checks.
var (
	_ RobotRepository = (*PostgresStore)(nil)
	_ ModelRepository = (*PostgresStore)(nil)
	_ CacheStore      = (*RedisStore)(nil)
)
