"""
Episode Extractor: Segments raw telemetry into training episodes.

Each episode is a coherent task execution window containing:
  - Observation frames (joint states, pose, battery)
  - Action sequences (commands issued during the window)
  - Outcome (success/failure based on task completion)

Input:  S3 raw NDJSON telemetry (telemetry/{date}/{robot_id}/batch_*.ndjson)
Output: S3 training episodes  (episodes/{date}/{robot_id}/episode_*.json)

Runs as a Spark batch job, scheduled via Kubernetes CronJob.
"""

import json
import os
import sys
from datetime import datetime, timedelta
from typing import Any

# Configuration from environment
S3_ENDPOINT = os.getenv("S3_ENDPOINT", "http://localhost:9000")
S3_ACCESS_KEY = os.getenv("S3_ACCESS_KEY", "fleetos")
S3_SECRET_KEY = os.getenv("S3_SECRET_KEY", "fleetos123")
S3_TELEMETRY_BUCKET = os.getenv("S3_BUCKET", "fleetos-telemetry")
S3_EPISODES_BUCKET = os.getenv("S3_EPISODES_BUCKET", "fleetos-training-data")

# Episode segmentation parameters
MIN_EPISODE_FRAMES = 10       # minimum frames for a valid episode
MAX_EPISODE_FRAMES = 500      # cap episode length
IDLE_GAP_MS = 5000            # gap > 5s between frames = episode boundary
BATTERY_DROP_THRESHOLD = 0.01 # min battery change to consider active


def segment_episodes(frames: list[dict[str, Any]]) -> list[list[dict[str, Any]]]:
    """Segment a sorted list of telemetry frames into episodes based on activity gaps."""
    if not frames:
        return []

    episodes: list[list[dict[str, Any]]] = []
    current_episode: list[dict[str, Any]] = [frames[0]]

    for i in range(1, len(frames)):
        prev_ts = frames[i - 1].get("ts", 0)
        curr_ts = frames[i].get("ts", 0)
        gap_ms = curr_ts - prev_ts

        # Start new episode on large time gap or status change
        if gap_ms > IDLE_GAP_MS or frames[i].get("status") != frames[i - 1].get("status"):
            if len(current_episode) >= MIN_EPISODE_FRAMES:
                episodes.append(current_episode[:MAX_EPISODE_FRAMES])
            current_episode = []

        current_episode.append(frames[i])

    # Don't forget the last episode
    if len(current_episode) >= MIN_EPISODE_FRAMES:
        episodes.append(current_episode[:MAX_EPISODE_FRAMES])

    return episodes


def compute_outcome(episode: list[dict[str, Any]]) -> dict[str, Any]:
    """Determine episode outcome based on telemetry signals."""
    if not episode:
        return {"outcome": "unknown", "quality_score": 0.0}

    first = episode[0]
    last = episode[-1]

    battery_drop = first.get("battery", 1.0) - last.get("battery", 1.0)
    had_error = any(f.get("status") == "error" for f in episode)

    # Simple heuristic: active episodes without errors are successful
    if had_error:
        outcome = "failure"
        quality = 0.2
    elif last.get("status") == "idle" and battery_drop > BATTERY_DROP_THRESHOLD:
        outcome = "success"
        quality = min(1.0, 0.5 + len(episode) / MAX_EPISODE_FRAMES)
    else:
        outcome = "partial"
        quality = 0.5

    return {
        "outcome": outcome,
        "quality_score": round(quality, 3),
        "battery_drop": round(battery_drop, 4),
        "frame_count": len(episode),
        "duration_ms": last.get("ts", 0) - first.get("ts", 0),
    }


def extract_training_pair(episode: list[dict[str, Any]]) -> dict[str, Any]:
    """Convert an episode into an observation-action training pair."""
    observations = []
    for frame in episode:
        obs = {
            "position": [frame.get("x", 0), frame.get("y", 0), frame.get("z", 0)],
            "battery": frame.get("battery", 0),
            "status": frame.get("status", "unknown"),
            "timestamp": frame.get("ts", 0),
        }
        observations.append(obs)

    # Actions are derived from position deltas between frames
    actions = []
    for i in range(1, len(observations)):
        dx = observations[i]["position"][0] - observations[i - 1]["position"][0]
        dy = observations[i]["position"][1] - observations[i - 1]["position"][1]
        dz = observations[i]["position"][2] - observations[i - 1]["position"][2]
        actions.append({"dx": round(dx, 6), "dy": round(dy, 6), "dz": round(dz, 6)})

    outcome = compute_outcome(episode)

    return {
        "robot_id": episode[0].get("robot_id", "unknown"),
        "episode_id": f"{episode[0].get('robot_id', 'unknown')}_{episode[0].get('ts', 0)}",
        "start_time": episode[0].get("ts", 0),
        "end_time": episode[-1].get("ts", 0),
        "observations": observations,
        "actions": actions,
        **outcome,
    }


def main():
    """Main entry point for Spark job execution."""
    try:
        from pyspark.sql import SparkSession
    except ImportError:
        print("PySpark not available, running in local mode for testing")
        # Local mode: process a single sample file
        return run_local_test()

    spark = (
        SparkSession.builder
        .appName("FleetOS Episode Extractor")
        .config("spark.hadoop.fs.s3a.endpoint", S3_ENDPOINT)
        .config("spark.hadoop.fs.s3a.access.key", S3_ACCESS_KEY)
        .config("spark.hadoop.fs.s3a.secret.key", S3_SECRET_KEY)
        .config("spark.hadoop.fs.s3a.path.style.access", "true")
        .config("spark.hadoop.fs.s3a.impl", "org.apache.hadoop.fs.s3a.S3AFileSystem")
        .getOrCreate()
    )

    # Process yesterday's telemetry by default
    yesterday = (datetime.utcnow() - timedelta(days=1)).strftime("%Y/%m/%d")
    input_path = f"s3a://{S3_TELEMETRY_BUCKET}/telemetry/{yesterday}/"

    print(f"Reading telemetry from: {input_path}")

    try:
        df = spark.read.json(input_path)
        robot_ids = [row.robot_id for row in df.select("robot_id").distinct().collect()]

        total_episodes = 0
        for robot_id in robot_ids:
            frames = [
                row.asDict()
                for row in df.filter(df.robot_id == robot_id).orderBy("ts").collect()
            ]

            episodes = segment_episodes(frames)
            for ep in episodes:
                training_pair = extract_training_pair(ep)
                episode_json = json.dumps(training_pair)

                output_key = (
                    f"s3a://{S3_EPISODES_BUCKET}/episodes/{yesterday}/"
                    f"{robot_id}/{training_pair['episode_id']}.json"
                )
                spark.sparkContext.parallelize([episode_json]).saveAsTextFile(output_key)
                total_episodes += 1

        print(f"Extracted {total_episodes} episodes from {len(robot_ids)} robots")

    except Exception as e:
        print(f"Episode extraction failed: {e}", file=sys.stderr)
        raise
    finally:
        spark.stop()


def run_local_test():
    """Run a local test with sample data."""
    sample_frames = [
        {"robot_id": "r1", "ts": 1000, "x": 0, "y": 0, "z": 0, "battery": 0.95, "status": "active"},
        {"robot_id": "r1", "ts": 1100, "x": 0.1, "y": 0, "z": 0, "battery": 0.94, "status": "active"},
        {"robot_id": "r1", "ts": 1200, "x": 0.2, "y": 0.1, "z": 0, "battery": 0.93, "status": "active"},
    ] * 5  # repeat to meet MIN_EPISODE_FRAMES

    episodes = segment_episodes(sample_frames)
    print(f"Segmented into {len(episodes)} episodes")

    for ep in episodes:
        pair = extract_training_pair(ep)
        print(f"Episode: {pair['episode_id']}, frames={pair['frame_count']}, outcome={pair['outcome']}")


if __name__ == "__main__":
    main()
