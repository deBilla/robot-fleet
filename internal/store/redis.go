package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore handles hot state caching and rate limiting.
type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(addr, password string, db int) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	slog.Info("connected to redis", "addr", addr)
	return &RedisStore{client: client}, nil
}

func (s *RedisStore) Close() {
	s.client.Close()
}

// RobotHotState is the cached current state of a robot.
type RobotHotState struct {
	RobotID      string  `json:"robot_id"`
	Status       string  `json:"status"`
	PosX         float64 `json:"pos_x"`
	PosY         float64 `json:"pos_y"`
	PosZ         float64 `json:"pos_z"`
	BatteryLevel float64 `json:"battery_level"`
	LastSeen     int64   `json:"last_seen"`

	// Rich sensor data (for ROS 2 bridge + dashboard)
	Joints        map[string]float64 `json:"joints,omitempty"`
	JointVelocity map[string]float64 `json:"joint_velocities,omitempty"`
	JointTorque   map[string]float64 `json:"joint_torques,omitempty"`
	BatteryV      float64            `json:"battery_voltage,omitempty"`
	CPUTemp       float64            `json:"cpu_temp,omitempty"`
	MotorTemp     float64            `json:"motor_temp,omitempty"`
	WiFiRSSI      int                `json:"wifi_rssi,omitempty"`
	UptimeSecs    int64              `json:"uptime_secs,omitempty"`
	DistTotal     float64            `json:"distance_total,omitempty"`
	FootForceL    float64            `json:"foot_force_left,omitempty"`
	FootForceR    float64            `json:"foot_force_right,omitempty"`
	OdomVelX      float64            `json:"odom_vel_x,omitempty"`
	OdomVelY      float64            `json:"odom_vel_y,omitempty"`
}

const (
	RobotStateTTL   = 5 * time.Minute  // TTL for cached robot hot state
	UsageCounterTTL = 48 * time.Hour   // TTL for daily usage counters
)

func robotKey(robotID string) string {
	return fmt.Sprintf("robot:state:%s", robotID)
}

// SetRobotState caches the current robot state with a TTL.
func (s *RedisStore) SetRobotState(ctx context.Context, state *RobotHotState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, robotKey(state.RobotID), data, RobotStateTTL).Err()
}

// GetRobotState retrieves the cached state for a robot.
func (s *RedisStore) GetRobotState(ctx context.Context, robotID string) (*RobotHotState, error) {
	data, err := s.client.Get(ctx, robotKey(robotID)).Bytes()
	if err != nil {
		return nil, err
	}
	var state RobotHotState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// CheckRateLimit implements a sliding window rate limiter.
// Returns (allowed bool, remaining int, resetTime time.Time).
func (s *RedisStore) CheckRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time, error) {
	now := time.Now()
	windowStart := now.Add(-window)

	pipe := s.client.Pipeline()
	// Remove expired entries
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart.UnixNano()))
	// Add current request
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixNano()), Member: now.UnixNano()})
	// Count requests in window
	countCmd := pipe.ZCard(ctx, key)
	// Set expiry on the key
	pipe.Expire(ctx, key, window)

	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, time.Time{}, err
	}

	count := int(countCmd.Val())
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	resetTime := now.Add(window)

	return count <= limit, remaining, resetTime, nil
}

// IncrementUsageCounter increments an API usage counter for billing.
func (s *RedisStore) IncrementUsageCounter(ctx context.Context, tenantID, metric string) (int64, error) {
	key := fmt.Sprintf("usage:%s:%s:%s", tenantID, metric, time.Now().Format("2006-01-02"))
	count, err := s.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	// Best-effort TTL — counter still valid without it, but prevents unbounded growth
	_ = s.client.Expire(ctx, key, UsageCounterTTL).Err()
	return count, nil
}

// GetUsageCounter retrieves the current usage count.
func (s *RedisStore) GetUsageCounter(ctx context.Context, tenantID, metric, date string) (int64, error) {
	key := fmt.Sprintf("usage:%s:%s:%s", tenantID, metric, date)
	return s.client.Get(ctx, key).Int64()
}

// PublishEvent publishes an event to a Redis pub/sub channel (for WebSocket fanout).
func (s *RedisStore) PublishEvent(ctx context.Context, channel string, data []byte) error {
	return s.client.Publish(ctx, channel, data).Err()
}

// Subscribe returns a pub/sub subscription for real-time events.
func (s *RedisStore) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return s.client.Subscribe(ctx, channels...)
}

// SetCacheJSON stores arbitrary JSON data with a TTL.
func (s *RedisStore) SetCacheJSON(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, data, ttl).Err()
}

// GetCacheJSON retrieves cached JSON data by key.
func (s *RedisStore) GetCacheJSON(ctx context.Context, key string) ([]byte, error) {
	return s.client.Get(ctx, key).Bytes()
}
