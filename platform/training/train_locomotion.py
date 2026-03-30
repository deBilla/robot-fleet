"""
SB3 PPO training script for Humanoid-v4 locomotion policy.

Runs as a Kubernetes Job / Kubeflow Pipeline component.
Uploads trained policy + metrics to S3, updates status via callback URL.

Usage:
    python train_locomotion.py \
        --job-id JOB_ID \
        --timesteps 2000000 \
        --s3-endpoint minio:9000 \
        --s3-bucket fleetos-models \
        --callback-url http://api:8080/api/v1/internal/training/callback
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path

import gymnasium as gym
import numpy as np
from stable_baselines3 import PPO
from stable_baselines3.common.callbacks import BaseCallback
from stable_baselines3.common.evaluation import evaluate_policy
from stable_baselines3.common.vec_env import DummyVecEnv, VecNormalize


class MetricsCallback(BaseCallback):
    """Logs training metrics to a JSON file for the platform to consume."""

    def __init__(self, metrics_path: str, verbose=0):
        super().__init__(verbose)
        self.metrics_path = metrics_path
        self.metrics = {
            "episode_rewards": [],
            "episode_lengths": [],
            "timestamps": [],
        }

    def _on_step(self) -> bool:
        if len(self.model.ep_info_buffer) > 0:
            ep_info = self.model.ep_info_buffer[-1]
            self.metrics["episode_rewards"].append(float(ep_info["r"]))
            self.metrics["episode_lengths"].append(int(ep_info["l"]))
            self.metrics["timestamps"].append(time.time())

            # Write metrics periodically (every 100 episodes)
            if len(self.metrics["episode_rewards"]) % 100 == 0:
                self._flush()
        return True

    def _flush(self):
        summary = {
            "total_episodes": len(self.metrics["episode_rewards"]),
            "mean_reward": float(np.mean(self.metrics["episode_rewards"][-100:])),
            "mean_length": float(np.mean(self.metrics["episode_lengths"][-100:])),
            "max_reward": float(np.max(self.metrics["episode_rewards"])),
            "total_timesteps": self.num_timesteps,
        }
        with open(self.metrics_path, "w") as f:
            json.dump(summary, f, indent=2)

    def _on_training_end(self):
        self._flush()


def make_env():
    """Create Humanoid-v4 environment."""
    return gym.make("Humanoid-v4")


def train(args):
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    print(f"[train] Starting PPO training on Humanoid-v4")
    print(f"[train] Job ID: {args.job_id}")
    print(f"[train] Timesteps: {args.timesteps}")
    print(f"[train] Output: {output_dir}")

    # Create vectorized environment with normalization
    env = DummyVecEnv([make_env])
    env = VecNormalize(env, norm_obs=True, norm_reward=True, clip_obs=10.0)

    # PPO hyperparameters tuned for Humanoid-v4
    model = PPO(
        "MlpPolicy",
        env,
        learning_rate=3e-4,
        n_steps=2048,
        batch_size=64,
        n_epochs=10,
        gamma=0.99,
        gae_lambda=0.95,
        clip_range=0.2,
        ent_coef=0.0,
        verbose=1,
        device=args.device,
        policy_kwargs=dict(
            net_arch=dict(pi=[256, 256], vf=[256, 256]),
        ),
    )

    # Train with metrics callback
    metrics_path = str(output_dir / "metrics.json")
    callback = MetricsCallback(metrics_path)

    start_time = time.time()
    model.learn(total_timesteps=args.timesteps, callback=callback)
    train_duration = time.time() - start_time

    # Save model
    model_path = str(output_dir / "policy")
    model.save(model_path)
    env.save(str(output_dir / "vec_normalize.pkl"))

    # Export to ONNX if possible
    onnx_path = None
    try:
        import torch
        dummy_obs = torch.randn(1, env.observation_space.shape[0])
        torch.onnx.export(
            model.policy,
            dummy_obs,
            str(output_dir / "policy.onnx"),
            input_names=["observation"],
            output_names=["action"],
            dynamic_axes={"observation": {0: "batch"}, "action": {0: "batch"}},
        )
        onnx_path = str(output_dir / "policy.onnx")
        print(f"[train] ONNX export successful: {onnx_path}")
    except Exception as e:
        print(f"[train] ONNX export skipped: {e}")

    # Evaluate final policy
    mean_reward, std_reward = evaluate_policy(model, env, n_eval_episodes=20)

    # Write final results
    results = {
        "job_id": args.job_id,
        "status": "completed",
        "model_path": model_path + ".zip",
        "artifact_url": f"training/{args.job_id}/policy.zip",
        "onnx_path": onnx_path,
        "train_duration_seconds": round(train_duration, 1),
        "total_timesteps": args.timesteps,
        "eval_mean_reward": round(float(mean_reward), 2),
        "eval_std_reward": round(float(std_reward), 2),
        "hyperparameters": {
            "algorithm": "PPO",
            "learning_rate": 3e-4,
            "n_steps": 2048,
            "batch_size": 64,
            "n_epochs": 10,
            "gamma": 0.99,
            "net_arch": [256, 256],
        },
    }
    with open(str(output_dir / "results.json"), "w") as f:
        json.dump(results, f, indent=2)

    print(f"[train] Training complete: reward={mean_reward:.1f}±{std_reward:.1f}, "
          f"duration={train_duration:.0f}s")

    # Upload to S3 if configured
    if args.s3_endpoint:
        upload_to_s3(args, output_dir, results)

    # Notify platform via callback
    if args.callback_url:
        notify_callback(args.callback_url, results)

    return results


def upload_to_s3(args, output_dir: Path, results: dict):
    """Upload training artifacts to S3/MinIO."""
    try:
        from minio import Minio

        client = Minio(
            args.s3_endpoint,
            access_key=args.s3_access_key,
            secret_key=args.s3_secret_key,
            secure=False,
        )

        bucket = args.s3_bucket
        if not client.bucket_exists(bucket):
            client.make_bucket(bucket)

        prefix = f"training/{args.job_id}"
        for fpath in output_dir.iterdir():
            key = f"{prefix}/{fpath.name}"
            client.fput_object(bucket, key, str(fpath))
            print(f"[s3] Uploaded {key}")

    except Exception as e:
        print(f"[s3] Upload failed (non-fatal): {e}")


def notify_callback(url: str, results: dict):
    """POST training results to the platform callback endpoint."""
    try:
        import urllib.request
        data = json.dumps(results).encode()
        req = urllib.request.Request(
            url, data=data, headers={"Content-Type": "application/json"}, method="POST"
        )
        urllib.request.urlopen(req, timeout=10)
        print(f"[callback] Notified {url}")
    except Exception as e:
        print(f"[callback] Failed (non-fatal): {e}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Train locomotion policy")
    parser.add_argument("--job-id", required=True, help="Training job ID")
    parser.add_argument("--timesteps", type=int, default=1_000_000)
    parser.add_argument("--device", default="auto", help="cpu, cuda, or auto")
    parser.add_argument("--output-dir", default="/tmp/training-output")
    parser.add_argument("--s3-endpoint", default=os.getenv("S3_ENDPOINT", ""))
    parser.add_argument("--s3-bucket", default=os.getenv("S3_BUCKET", "fleetos-models"))
    parser.add_argument("--s3-access-key", default=os.getenv("S3_ACCESS_KEY", "fleetos"))
    parser.add_argument("--s3-secret-key", default=os.getenv("S3_SECRET_KEY", "fleetos123"))
    parser.add_argument("--callback-url", default=os.getenv("TRAINING_CALLBACK_URL", ""))
    args = parser.parse_args()

    train(args)
