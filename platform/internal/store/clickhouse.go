package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// AnalyticsStore defines the interface for querying OLAP analytics data.
type AnalyticsStore interface {
	GetRobotHourly(ctx context.Context, robotID string, from, to time.Time) ([]RobotHourlyMetric, error)
	GetFleetHourly(ctx context.Context, from, to time.Time) ([]FleetHourlyMetric, error)
	GetAnomalies(ctx context.Context, from, to time.Time) ([]RobotAnomaly, error)
	Close()
}

// RobotHourlyMetric represents a per-robot hourly aggregate from ClickHouse.
type RobotHourlyMetric struct {
	RobotID        string    `json:"robot_id"`
	Hour           time.Time `json:"hour"`
	AvgBattery     float64   `json:"avg_battery"`
	MinBattery     float64   `json:"min_battery"`
	MaxBattery     float64   `json:"max_battery"`
	AvgX           float64   `json:"avg_x"`
	AvgY           float64   `json:"avg_y"`
	TotalEvents    uint64    `json:"total_events"`
	ActiveCount    uint64    `json:"active_count"`
	ErrorCount     uint64    `json:"error_count"`
	ChargingCount  uint64    `json:"charging_count"`
	ErrorRate      float64   `json:"error_rate"`
	MovementRangeX float64   `json:"movement_range_x"`
	MovementRangeY float64   `json:"movement_range_y"`
}

// FleetHourlyMetric represents a fleet-wide hourly aggregate.
type FleetHourlyMetric struct {
	Hour         time.Time `json:"hour"`
	UniqueRobots uint32    `json:"unique_robots"`
	TotalEvents  uint64    `json:"total_events"`
	AvgBattery   float64   `json:"avg_battery"`
	ActiveEvents uint64    `json:"active_events"`
	ErrorEvents  uint64    `json:"error_events"`
	ErrorRate    float64   `json:"error_rate"`
}

// RobotAnomaly represents a detected anomaly.
type RobotAnomaly struct {
	RobotID            string    `json:"robot_id"`
	DetectedAt         time.Time `json:"detected_at"`
	BatteryDrop        float64   `json:"battery_drop"`
	ErrorRate          float64   `json:"error_rate"`
	PositionVolatility float64   `json:"position_volatility"`
}

const clickHouseMaxExecutionTimeSec = 60

// ClickHouseStore implements AnalyticsStore using ClickHouse.
type ClickHouseStore struct {
	conn clickhouse.Conn
}

// NewClickHouseStore creates a new ClickHouse connection.
func NewClickHouseStore(ctx context.Context, addr string) (*ClickHouseStore, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Settings: clickhouse.Settings{
			"max_execution_time": clickHouseMaxExecutionTimeSec,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}

	slog.Info("connected to clickhouse", "addr", addr)
	return &ClickHouseStore{conn: conn}, nil
}

func (s *ClickHouseStore) Close() {
	s.conn.Close()
}

func (s *ClickHouseStore) GetRobotHourly(ctx context.Context, robotID string, from, to time.Time) ([]RobotHourlyMetric, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT robot_id, hour, avg_battery, min_battery, max_battery, avg_x, avg_y,
		       total_events, active_count, error_count, charging_count, error_rate,
		       movement_range_x, movement_range_y
		FROM robot_telemetry_hourly
		WHERE robot_id = $1 AND hour >= $2 AND hour <= $3
		ORDER BY hour
	`, robotID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query robot hourly: %w", err)
	}
	defer rows.Close()

	var metrics []RobotHourlyMetric
	for rows.Next() {
		var m RobotHourlyMetric
		if err := rows.Scan(&m.RobotID, &m.Hour, &m.AvgBattery, &m.MinBattery, &m.MaxBattery,
			&m.AvgX, &m.AvgY, &m.TotalEvents, &m.ActiveCount, &m.ErrorCount, &m.ChargingCount,
			&m.ErrorRate, &m.MovementRangeX, &m.MovementRangeY); err != nil {
			return nil, fmt.Errorf("scan robot hourly: %w", err)
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

func (s *ClickHouseStore) GetFleetHourly(ctx context.Context, from, to time.Time) ([]FleetHourlyMetric, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT hour, unique_robots, total_events, avg_battery, active_events, error_events, error_rate
		FROM fleet_metrics_hourly
		WHERE hour >= $1 AND hour <= $2
		ORDER BY hour
	`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query fleet hourly: %w", err)
	}
	defer rows.Close()

	var metrics []FleetHourlyMetric
	for rows.Next() {
		var m FleetHourlyMetric
		if err := rows.Scan(&m.Hour, &m.UniqueRobots, &m.TotalEvents, &m.AvgBattery,
			&m.ActiveEvents, &m.ErrorEvents, &m.ErrorRate); err != nil {
			return nil, fmt.Errorf("scan fleet hourly: %w", err)
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

func (s *ClickHouseStore) GetAnomalies(ctx context.Context, from, to time.Time) ([]RobotAnomaly, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT robot_id, detected_at, battery_drop, error_rate, position_volatility
		FROM robot_anomalies
		WHERE detected_at >= $1 AND detected_at <= $2
		ORDER BY detected_at DESC
	`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query anomalies: %w", err)
	}
	defer rows.Close()

	var anomalies []RobotAnomaly
	for rows.Next() {
		var a RobotAnomaly
		if err := rows.Scan(&a.RobotID, &a.DetectedAt, &a.BatteryDrop, &a.ErrorRate, &a.PositionVolatility); err != nil {
			return nil, fmt.Errorf("scan anomaly: %w", err)
		}
		anomalies = append(anomalies, a)
	}
	return anomalies, nil
}

// Compile-time interface check.
var _ AnalyticsStore = (*ClickHouseStore)(nil)
