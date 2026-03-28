-- FleetOS Analytics — ClickHouse OLAP Schema
-- These tables are populated by Spark batch jobs reading from S3/MinIO

CREATE TABLE IF NOT EXISTS robot_telemetry_hourly (
    robot_id       String,
    hour           DateTime,
    avg_battery    Float64,
    min_battery    Float64,
    max_battery    Float64,
    avg_x          Float64,
    avg_y          Float64,
    total_events   UInt64,
    active_count   UInt64,
    error_count    UInt64,
    charging_count UInt64,
    error_rate     Float64,
    movement_range_x Float64,
    movement_range_y Float64
) ENGINE = ReplacingMergeTree()
ORDER BY (robot_id, hour);

CREATE TABLE IF NOT EXISTS fleet_metrics_hourly (
    hour           DateTime,
    unique_robots  UInt32,
    total_events   UInt64,
    avg_battery    Float64,
    active_events  UInt64,
    error_events   UInt64,
    error_rate     Float64
) ENGINE = ReplacingMergeTree()
ORDER BY hour;

CREATE TABLE IF NOT EXISTS robot_anomalies (
    robot_id            String,
    detected_at         DateTime,
    battery_drop        Float64,
    error_rate          Float64,
    position_volatility Float64,
    total_events        UInt64
) ENGINE = ReplacingMergeTree()
ORDER BY (detected_at, robot_id);
