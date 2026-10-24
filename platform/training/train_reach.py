"""
SB3 SAC+HER training script for FleetOS humanoid reaching.

Trains a goal-conditioned policy that moves the right hand to
arbitrary 3D target positions. Uses Hindsight Experience Replay
for sample-efficient learning from sparse rewards.

Usage:
    python train_reach.py \
        --job-id REACH_JOB_01 \
        --timesteps 500000 \
        --s3-endpoint minio:9000 \
        --s3-bucket fleetos-models

    # Fine-tune from existing reach policy
    python train_reach.py \
        --job-id REACH_JOB_02 \
        --timesteps 200000 \
        --base-model training/REACH_JOB_01/policy.zip
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path

import gymnasium as gym
import numpy as np
from policy_action import ABSOLUTE
from policy_meta import write_policy_meta
from stable_baselines3 import SAC
from stable_baselines3.common.callbacks import BaseCallback
from stable_baselines3.common.vec_env import DummyVecEnv
from stable_baselines3.her import HerReplayBuffer

from fleetos_reach_env import register_reach_env

register_reach_env()

PUSHGATEWAY_URL = os.environ.get("PUSHGATEWAY_URL", "")


def push_metrics(job_id: str, metrics: dict):
    """Push training metrics to Prometheus Pushgateway (if configured)."""
    if not PUSHGATEWAY_URL:
        return
    try:
        from prometheus_client import CollectorRegistry, Gauge, push_to_gateway
        registry = CollectorRegistry()
        for key, value in metrics.items():
            g = Gauge(f"fleetos_training_{key}", key, ["job_id"], registry=registry)
            g.labels(job_id=job_id).set(value)
        push_to_gateway(PUSHGATEWAY_URL, job=f"training-{job_id}", registry=registry)
    except Exception as e:
        print(f"[pushgateway] Failed: {e}")


class ReachMetricsCallback(BaseCallback):
    """Logs reach-specific metrics: success rate, distance to goal."""

    def __init__(self, metrics_path: str, job_id: str = "", verbose=0):
        super().__init__(verbose)
        self.metrics_path = metrics_path
        self.job_id = job_id
        self.successes = []
        self.episode_count = 0

    def _on_step(self) -> bool:
        if self.locals.get("infos"):
            for info in self.locals["infos"]:
                if "is_success" in info:
                    self.successes.append(float(info["is_success"]))
                    self.episode_count += 1

                    if self.episode_count % 50 == 0:
                        self._flush()
        return True

    def _flush(self):
        recent = self.successes[-100:] if self.successes else []
        summary = {
            "total_episodes": self.episode_count,
            "success_rate": float(np.mean(recent)) if recent else 0.0,
            "total_successes": int(sum(self.successes)),
            "total_timesteps": self.num_timesteps,
        }
        with open(self.metrics_path, "w") as f:
            json.dump(summary, f, indent=2)

        push_metrics(self.job_id, {
            "success_rate": summary["success_rate"],
            "timesteps": self.num_timesteps,
            "episodes": self.episode_count,
        })

        if self.verbose:
            print(f"[reach] ep={self.episode_count} "
                  f"success_rate={summary['success_rate']:.1%} "
                  f"steps={self.num_timesteps}")

    def _on_training_end(self):
        self._flush()


def train(args):
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    env_id = "FleetOS-Reach-v1"
    print(f"[reach] Starting SAC+HER training on {env_id}")
    print(f"[reach] Job ID: {args.job_id}")
    print(f"[reach] Timesteps: {args.timesteps}")

    env = DummyVecEnv([lambda: gym.make(env_id)])

    if args.base_model:
        base_path = _resolve_model_path(args, args.base_model)
        if base_path:
            print(f"[reach] Fine-tuning from: {base_path}")
            model = SAC.load(base_path, env=env, device=args.device)
        else:
            print(f"[reach] Base model not found, training from scratch")
            model = _create_model(env, args)
    else:
        model = _create_model(env, args)

    metrics_path = str(output_dir / "metrics.json")
    callback = ReachMetricsCallback(metrics_path, job_id=args.job_id, verbose=1)

    start_time = time.time()
    model.learn(total_timesteps=args.timesteps, callback=callback)
    train_duration = time.time() - start_time

    # Save
    model_path = str(output_dir / "policy")
    model.save(model_path)
    write_policy_meta(output_dir, ABSOLUTE)  # obs + action version, checked before execution

    # Evaluate
    success_count = 0
    eval_episodes = 50
    eval_env = gym.make(env_id)
    for _ in range(eval_episodes):
        obs, _ = eval_env.reset()
        done = False
        while not done:
            action, _ = model.predict(obs, deterministic=True)
            obs, _, terminated, truncated, info = eval_env.step(action)
            done = terminated or truncated
        if info.get("is_success"):
            success_count += 1
    eval_env.close()

    success_rate = success_count / eval_episodes

    results = {
        "job_id": args.job_id,
        "status": "completed",
        "task_type": "reach",
        "algorithm": "SAC+HER",
        "model_path": model_path + ".zip",
        "artifact_url": f"training/{args.job_id}/policy.zip",
        "environment": env_id,
        "train_duration_seconds": round(train_duration, 1),
        "total_timesteps": args.timesteps,
        "eval_success_rate": round(success_rate, 3),
        "eval_episodes": eval_episodes,
        "hyperparameters": {
            "algorithm": "SAC",
            "learning_rate": args.learning_rate,
            "buffer_size": args.buffer_size,
            "batch_size": args.batch_size,
            "her_n_sampled_goal": 4,
            "her_strategy": "future",
            "net_arch": [256, 256],
            "distance_threshold": 0.05,
        },
    }
    with open(str(output_dir / "results.json"), "w") as f:
        json.dump(results, f, indent=2)

    print(f"[reach] Training complete: success_rate={success_rate:.1%}, "
          f"duration={train_duration:.0f}s")

    if args.s3_endpoint:
        _upload_to_s3(args, output_dir)

    if args.callback_url:
        _notify_callback(args.callback_url, results)

    return results


def _create_model(env, args) -> SAC:
    """Create SAC model with HER replay buffer."""
    return SAC(
        "MultiInputPolicy",
        env,
        replay_buffer_class=HerReplayBuffer,
        replay_buffer_kwargs=dict(
            n_sampled_goal=4,
            goal_selection_strategy="future",
        ),
        learning_rate=args.learning_rate,
        buffer_size=args.buffer_size,
        batch_size=args.batch_size,
        gamma=0.95,
        tau=0.05,
        learning_starts=1000,
        verbose=1,
        device=args.device,
        policy_kwargs=dict(
            net_arch=[256, 256],
        ),
    )


def _resolve_model_path(args, artifact_url: str) -> str | None:
    if not args.s3_endpoint:
        return None
    try:
        from minio import Minio
        client = Minio(args.s3_endpoint, access_key=args.s3_access_key,
                       secret_key=args.s3_secret_key, secure=False)
        local_path = Path(args.output_dir) / "base_policy.zip"
        key = artifact_url.replace(f"s3://{args.s3_bucket}/", "")
        client.fget_object(args.s3_bucket, key, str(local_path))
        return str(local_path).replace(".zip", "")
    except Exception as e:
        print(f"[s3] Failed to download base model: {e}")
        return None


def _upload_to_s3(args, output_dir: Path):
    try:
        from minio import Minio
        client = Minio(args.s3_endpoint, access_key=args.s3_access_key,
                       secret_key=args.s3_secret_key, secure=False)
        if not client.bucket_exists(args.s3_bucket):
            client.make_bucket(args.s3_bucket)
        for f in output_dir.iterdir():
            if f.is_file():
                key = f"training/{args.job_id}/{f.name}"
                client.fput_object(args.s3_bucket, key, str(f))
                print(f"[s3] Uploaded {key}")
    except Exception as e:
        # Fatal: downstream eval/registry need training/<job>/policy.zip.
        print(f"[s3] Upload failed: {e}")
        raise


def _notify_callback(url: str, results: dict):
    try:
        import urllib.request
        req = urllib.request.Request(
            url, data=json.dumps(results).encode(),
            headers={"Content-Type": "application/json"}, method="POST",
        )
        urllib.request.urlopen(req, timeout=10)
        print(f"[callback] Notified {url}")
    except Exception as e:
        print(f"[callback] Failed: {e}")


def parse_args():
    p = argparse.ArgumentParser(description="Train reaching policy with SAC+HER")
    p.add_argument("--job-id", default=f"reach-{int(time.time())}")
    p.add_argument("--timesteps", type=int, default=500_000)
    p.add_argument("--learning-rate", type=float, default=3e-4)
    p.add_argument("--buffer-size", type=int, default=1_000_000)
    p.add_argument("--batch-size", type=int, default=256)
    p.add_argument("--base-model", default=None)
    p.add_argument("--output-dir", default="training_output")
    p.add_argument("--device", default="auto")
    p.add_argument("--s3-endpoint", default=os.environ.get("S3_ENDPOINT", ""))
    p.add_argument("--s3-bucket", default=os.environ.get("S3_BUCKET", "fleetos-models"))
    p.add_argument("--s3-access-key", default=os.environ.get("S3_ACCESS_KEY", "fleetos"))
    p.add_argument("--s3-secret-key", default=os.environ.get("S3_SECRET_KEY", "fleetos123"))
    p.add_argument("--callback-url", default=os.environ.get("TRAINING_CALLBACK_URL", ""))
    return p.parse_args()


if __name__ == "__main__":
    args = parse_args()
    train(args)
