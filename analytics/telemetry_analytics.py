"""
FleetOS Telemetry Analytics — Apache Spark Batch Job

Reads raw telemetry protobuf files from S3/MinIO, deserializes them,
and produces aggregated analytics:

1. Per-robot summary (avg battery, distance traveled, uptime)
2. Fleet-wide metrics by hour (active robots, error rate, avg battery)
3. Anomaly detection (robots with abnormal battery drain or position jumps)

Usage:
  # Submit to Spark cluster
  spark-submit --master spark://spark-master:7077 \
    --packages org.apache.hadoop:hadoop-aws:3.3.4,com.amazonaws:aws-java-sdk-bundle:1.12.262 \
    /opt/spark-jobs/telemetry_analytics.py

  # Or run locally for testing
  python telemetry_analytics.py --local
"""

from __future__ import annotations

import argparse
import json
import math
import struct
import sys
from datetime import datetime, timedelta

from pyspark.sql import SparkSession, Row
from pyspark.sql import functions as F
from pyspark.sql.types import (
    StructType, StructField, StringType, DoubleType,
    LongType, TimestampType, ArrayType,
)


# --- Configuration ---

MINIO_ENDPOINT = "http://minio:9000"
MINIO_ACCESS_KEY = "fleetos"
MINIO_SECRET_KEY = "fleetos123"
TELEMETRY_BUCKET = "fleetos-telemetry"
RESULTS_BUCKET = "fleetos-telemetry"

# ClickHouse
CLICKHOUSE_HOST = "clickhouse"
CLICKHOUSE_PORT = 8123
CLICKHOUSE_URL = f"http://{CLICKHOUSE_HOST}:{CLICKHOUSE_PORT}"


def create_spark_session(local: bool = False) -> SparkSession:
    """Create Spark session with S3/MinIO configuration."""
    builder = SparkSession.builder.appName("FleetOS Telemetry Analytics")

    if local:
        builder = builder.master("local[*]")

    spark = (
        builder
        .config("spark.hadoop.fs.s3a.endpoint", MINIO_ENDPOINT)
        .config("spark.hadoop.fs.s3a.access.key", MINIO_ACCESS_KEY)
        .config("spark.hadoop.fs.s3a.secret.key", MINIO_SECRET_KEY)
        .config("spark.hadoop.fs.s3a.path.style.access", "true")
        .config("spark.hadoop.fs.s3a.impl", "org.apache.hadoop.fs.s3a.S3AFileSystem")
        .config("spark.hadoop.fs.s3a.connection.ssl.enabled", "false")
        .getOrCreate()
    )

    spark.sparkContext.setLogLevel("WARN")
    return spark


# --- Protobuf Parsing ---
# Since we can't use compiled protobuf in PySpark easily,
# we parse the raw protobuf wire format for the fields we need.
# This is a lightweight approach that avoids adding protobuf as a Spark dependency.

def parse_telemetry_proto(data: bytes) -> dict | None:
    """
    Parse a TelemetryPacket protobuf into a dict.

    Wire format (proto3):
      field 1 (robot_id): string
      field 2 (state): embedded RobotState message
        - field 1 (robot_id): string
        - field 3 (status): string
        - field 4 (battery_level): double
        - field 5 (pose): Pose message
          - field 1 (position): Vector3 {x, y, z as doubles}
    """
    try:
        result = {"robot_id": "", "status": "", "battery": 0.0, "x": 0.0, "y": 0.0, "z": 0.0}
        pos = 0

        while pos < len(data):
            if pos >= len(data):
                break
            # Read varint tag
            tag_byte = data[pos]
            pos += 1
            field_num = tag_byte >> 3
            wire_type = tag_byte & 0x07

            if wire_type == 0:  # varint
                while pos < len(data) and data[pos] & 0x80:
                    pos += 1
                pos += 1
            elif wire_type == 1:  # 64-bit
                pos += 8
            elif wire_type == 2:  # length-delimited
                length = 0
                shift = 0
                while pos < len(data):
                    b = data[pos]
                    pos += 1
                    length |= (b & 0x7F) << shift
                    if not (b & 0x80):
                        break
                    shift += 7

                if field_num == 1:  # robot_id
                    result["robot_id"] = data[pos:pos + length].decode("utf-8", errors="replace")
                elif field_num == 2:  # state (embedded message)
                    state_data = data[pos:pos + length]
                    _parse_state(state_data, result)

                pos += length
            elif wire_type == 5:  # 32-bit
                pos += 4
            else:
                break  # unknown wire type

        if result["robot_id"]:
            return result
        return None
    except Exception:
        return None


def _parse_state(data: bytes, result: dict):
    """Parse the embedded RobotState message."""
    pos = 0
    while pos < len(data):
        if pos >= len(data):
            break
        tag_byte = data[pos]
        pos += 1
        field_num = tag_byte >> 3
        wire_type = tag_byte & 0x07

        if wire_type == 0:  # varint
            while pos < len(data) and data[pos] & 0x80:
                pos += 1
            pos += 1
        elif wire_type == 1:  # 64-bit (double)
            if field_num == 4:  # battery_level
                result["battery"] = struct.unpack("<d", data[pos:pos + 8])[0]
            pos += 8
        elif wire_type == 2:  # length-delimited
            length = 0
            shift = 0
            while pos < len(data):
                b = data[pos]
                pos += 1
                length |= (b & 0x7F) << shift
                if not (b & 0x80):
                    break
                shift += 7

            if field_num == 1:  # robot_id in state
                result["robot_id"] = data[pos:pos + length].decode("utf-8", errors="replace")
            elif field_num == 3:  # status
                result["status"] = data[pos:pos + length].decode("utf-8", errors="replace")
            elif field_num == 5:  # pose (embedded)
                _parse_pose(data[pos:pos + length], result)

            pos += length
        elif wire_type == 5:  # 32-bit
            pos += 4
        else:
            break


def _parse_pose(data: bytes, result: dict):
    """Parse Pose → Position (x, y, z doubles)."""
    pos = 0
    while pos < len(data):
        if pos >= len(data):
            break
        tag_byte = data[pos]
        pos += 1
        field_num = tag_byte >> 3
        wire_type = tag_byte & 0x07

        if wire_type == 2:  # length-delimited (position submessage)
            length = 0
            shift = 0
            while pos < len(data):
                b = data[pos]
                pos += 1
                length |= (b & 0x7F) << shift
                if not (b & 0x80):
                    break
                shift += 7

            if field_num == 1:  # position Vector3
                _parse_vector3(data[pos:pos + length], result)
            pos += length
        elif wire_type == 1:  # 64-bit
            pos += 8
        elif wire_type == 0:  # varint
            while pos < len(data) and data[pos] & 0x80:
                pos += 1
            pos += 1
        else:
            break


def _parse_vector3(data: bytes, result: dict):
    """Parse Vector3 (x=1, y=2, z=3 as doubles)."""
    pos = 0
    while pos < len(data):
        if pos >= len(data):
            break
        tag_byte = data[pos]
        pos += 1
        field_num = tag_byte >> 3
        wire_type = tag_byte & 0x07

        if wire_type == 1:  # 64-bit double
            val = struct.unpack("<d", data[pos:pos + 8])[0]
            if field_num == 1:
                result["x"] = val
            elif field_num == 2:
                result["y"] = val
            elif field_num == 3:
                result["z"] = val
            pos += 8
        else:
            break


# --- Analytics Jobs ---

def load_telemetry(spark: SparkSession, date: str | None = None) -> "DataFrame":
    """Load telemetry NDJSON batches from S3."""
    if date is None:
        date = datetime.utcnow().strftime("%Y/%m/%d")

    base_path = f"s3a://{TELEMETRY_BUCKET}/telemetry/{date}"
    print(f"Loading telemetry from: {base_path}")

    schema = StructType([
        StructField("robot_id", StringType(), False),
        StructField("status", StringType(), True),
        StructField("battery", DoubleType(), True),
        StructField("x", DoubleType(), True),
        StructField("y", DoubleType(), True),
        StructField("z", DoubleType(), True),
        StructField("ts", LongType(), True),
    ])

    # Read NDJSON batch files recursively
    df = (
        spark.read.schema(schema)
        .option("recursiveFileLookup", "true")
        .json(base_path)
        .withColumn("timestamp", F.from_unixtime(F.col("ts") / 1000).cast(TimestampType()))
        .drop("ts")
        .filter(F.col("robot_id").isNotNull())
    )

    record_count = df.count()
    print(f"Loaded {record_count} telemetry records")
    return df


def robot_summary(df: "DataFrame") -> "DataFrame":
    """Per-robot summary: avg battery, position range, status distribution."""
    summary = (
        df.groupBy("robot_id")
        .agg(
            F.count("*").alias("total_events"),
            F.avg("battery").alias("avg_battery"),
            F.min("battery").alias("min_battery"),
            F.max("battery").alias("max_battery"),
            F.avg("x").alias("avg_x"),
            F.avg("y").alias("avg_y"),
            F.min("x").alias("min_x"),
            F.max("x").alias("max_x"),
            F.min("y").alias("min_y"),
            F.max("y").alias("max_y"),
            F.count(F.when(F.col("status") == "active", 1)).alias("active_count"),
            F.count(F.when(F.col("status") == "error", 1)).alias("error_count"),
            F.count(F.when(F.col("status") == "charging", 1)).alias("charging_count"),
        )
        .withColumn("error_rate", F.col("error_count") / F.col("total_events"))
        .withColumn("movement_range_x", F.col("max_x") - F.col("min_x"))
        .withColumn("movement_range_y", F.col("max_y") - F.col("min_y"))
        .orderBy("robot_id")
    )
    return summary


def fleet_hourly_metrics(df: "DataFrame") -> "DataFrame":
    """Fleet-wide metrics aggregated by hour."""
    hourly = (
        df
        .withColumn("hour", F.date_trunc("hour", F.col("timestamp")))
        .groupBy("hour")
        .agg(
            F.countDistinct("robot_id").alias("unique_robots"),
            F.count("*").alias("total_events"),
            F.avg("battery").alias("avg_battery"),
            F.count(F.when(F.col("status") == "active", 1)).alias("active_events"),
            F.count(F.when(F.col("status") == "error", 1)).alias("error_events"),
        )
        .withColumn("error_rate", F.col("error_events") / F.col("total_events"))
        .orderBy("hour")
    )
    return hourly


def detect_anomalies(df: "DataFrame") -> "DataFrame":
    """Detect robots with anomalous behavior: rapid battery drain or position jumps."""
    # Calculate per-robot battery change rate
    anomalies = (
        df.groupBy("robot_id")
        .agg(
            F.min("battery").alias("min_battery"),
            F.max("battery").alias("max_battery"),
            F.count(F.when(F.col("status") == "error", 1)).alias("error_count"),
            F.count("*").alias("total_events"),
            F.stddev("x").alias("x_stddev"),
            F.stddev("y").alias("y_stddev"),
        )
        .withColumn("battery_drop", F.col("max_battery") - F.col("min_battery"))
        .withColumn("error_rate", F.col("error_count") / F.col("total_events"))
        .withColumn("position_volatility", F.col("x_stddev") + F.col("y_stddev"))
        .filter(
            (F.col("battery_drop") > 0.5) |    # >50% battery drop
            (F.col("error_rate") > 0.1) |       # >10% error rate
            (F.col("position_volatility") > 20)  # high position variance
        )
        .orderBy(F.desc("error_rate"))
    )
    return anomalies


def save_results(df: "DataFrame", name: str, date: str):
    """Save analytics results to S3 as JSON."""
    output_path = f"s3a://{RESULTS_BUCKET}/analytics/{date}/{name}"
    df.coalesce(1).write.mode("overwrite").json(output_path)
    print(f"Saved {name} to S3: {output_path}")


# --- ClickHouse Writer ---

def write_to_clickhouse(df: "DataFrame", table: str):
    """Write a Spark DataFrame to ClickHouse via HTTP INSERT."""
    import urllib.request

    rows = df.collect()
    if not rows:
        print(f"No rows to write to ClickHouse table {table}")
        return

    columns = df.columns
    values_parts = []
    for row in rows:
        vals = []
        for col in columns:
            v = row[col]
            if v is None:
                vals.append("0")
            elif isinstance(v, str):
                vals.append(f"'{v}'")
            elif isinstance(v, datetime):
                vals.append(f"'{v.strftime('%Y-%m-%d %H:%M:%S')}'")
            else:
                vals.append(str(v))
        values_parts.append(f"({','.join(vals)})")

    sql = f"INSERT INTO {table} ({','.join(columns)}) VALUES {','.join(values_parts)}"

    req = urllib.request.Request(
        CLICKHOUSE_URL,
        data=sql.encode(),
        headers={"Content-Type": "text/plain"},
    )
    try:
        urllib.request.urlopen(req)
        print(f"Wrote {len(rows)} rows to ClickHouse table {table}")
    except Exception as e:
        print(f"WARNING: Failed to write to ClickHouse {table}: {e}")


# --- Main ---

def main():
    parser = argparse.ArgumentParser(description="FleetOS Telemetry Analytics")
    parser.add_argument("--local", action="store_true", help="Run in local mode")
    parser.add_argument("--date", type=str, default=None, help="Date to analyze (YYYY/MM/DD)")
    args = parser.parse_args()

    date = args.date or datetime.utcnow().strftime("%Y/%m/%d")
    print(f"=" * 60)
    print(f"FleetOS Telemetry Analytics")
    print(f"Date: {date}")
    print(f"Mode: {'local' if args.local else 'cluster'}")
    print(f"=" * 60)

    spark = create_spark_session(local=args.local)

    try:
        df = load_telemetry(spark, date)

        if df.count() == 0:
            print("No telemetry data found for this date. Exiting.")
            return

        # Robot summary → S3 + ClickHouse
        print("\n--- Robot Summary ---")
        summary = robot_summary(df)
        summary.show(truncate=False)
        save_results(summary, "robot_summary", date.replace("/", "-"))

        # For ClickHouse: select matching columns + add hour
        ch_summary = (
            summary
            .withColumn("hour", F.lit(datetime.strptime(date, "%Y/%m/%d")))
            .select("robot_id", "hour", "avg_battery", "min_battery", "max_battery",
                    "avg_x", "avg_y", "total_events", "active_count", "error_count",
                    "charging_count", "error_rate", "movement_range_x", "movement_range_y")
        )
        write_to_clickhouse(ch_summary, "robot_telemetry_hourly")

        # Fleet hourly → S3 + ClickHouse
        print("\n--- Fleet Hourly Metrics ---")
        hourly = fleet_hourly_metrics(df)
        hourly.show(truncate=False)
        save_results(hourly, "fleet_hourly", date.replace("/", "-"))
        write_to_clickhouse(hourly, "fleet_metrics_hourly")

        # Anomalies → S3 + ClickHouse
        print("\n--- Anomaly Detection ---")
        anomalies = detect_anomalies(df)
        if anomalies.count() > 0:
            anomalies.show(truncate=False)
            save_results(anomalies, "anomalies", date.replace("/", "-"))
            ch_anomalies = anomalies.withColumn("detected_at", F.lit(datetime.utcnow()))
            write_to_clickhouse(ch_anomalies, "robot_anomalies")
        else:
            print("No anomalies detected.")

        print(f"\nAnalytics complete.")
        print(f"  S3: s3://{RESULTS_BUCKET}/analytics/")
        print(f"  ClickHouse: {CLICKHOUSE_URL}")

    finally:
        spark.stop()


if __name__ == "__main__":
    main()
