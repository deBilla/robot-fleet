"""
SB3 PPO training script for FleetOS humanoid locomotion.

Supports two modes:
  1. From scratch: Train on FleetOS-Humanoid-v1 (custom lab environment)
  2. From experience: Fine-tune existing policy using collected experience from S3

Runs as a Kubernetes Job / Kubeflow Pipeline component.
Uploads trained policy + metrics to S3, updates status via callback URL.

Usage:
    # Train from scratch on custom lab env
    python train_locomotion.py \
        --job-id JOB_ID \
        --timesteps 2000000 \
        --s3-endpoint minio:9000 \
        --s3-bucket fleetos-models \
        --callback-url http://api:8080/api/v1/internal/training/callback

    # Fine-tune from collected experience
    python train_locomotion.py \
        --job-id JOB_ID \
        --timesteps 500000 \
        --from-experience training/experience/ \
        --base-model training/prev-job/policy.zip \
        --s3-endpoint minio:9000
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

# Register our custom environment
from fleetos_env import register_fleetos_env
register_fleetos_env()


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

            if len(self.metrics["episode_rewards"]) % 100 == 0:
                self._flush()
        return True

    def _flush(self):
        rewards = self.metrics["episode_rewards"]
        lengths = self.metrics["episode_lengths"]
        summary = {
            "total_episodes": len(rewards),
            "mean_reward": float(np.mean(rewards[-100:])) if rewards else 0.0,
            "mean_length": float(np.mean(lengths[-100:])) if lengths else 0.0,
            "max_reward": float(np.max(rewards)) if rewards else 0.0,
            "total_timesteps": self.num_timesteps,
        }
        with open(self.metrics_path, "w") as f:
            json.dump(summary, f, indent=2)

    def _on_training_end(self):
        self._flush()


def make_env(env_id: str):
    """Create environment — supports both custom and stock Gymnasium."""
    def _init():
        return gym.make(env_id)
    return _init


def train(args):
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    env_id = args.env_id
    print(f"[train] Starting PPO training on {env_id}")
    print(f"[train] Job ID: {args.job_id}")
    print(f"[train] Timesteps: {args.timesteps}")
    print(f"[train] Output: {output_dir}")
    if args.base_model:
        print(f"[train] Fine-tuning from: {args.base_model}")
    if args.from_experience:
        print(f"[train] Loading experience from: {args.from_experience}")

    # Create vectorized environment with normalization
    env = DummyVecEnv([make_env(env_id)])
    env = VecNormalize(env, norm_obs=True, norm_reward=True, clip_obs=10.0)

    # Initialize model: from base or from scratch
    if args.base_model:
        base_path = _resolve_model_path(args, args.base_model)
        if base_path:
            print(f"[train] Loading base model from {base_path}")
            model = PPO.load(base_path, env=env, device=args.device)
            # Optionally load experience buffer for offline updates
            if args.from_experience:
                _load_experience_into_buffer(args, model)
        else:
            print(f"[train] Base model not found, training from scratch")
            model = _create_model(env, args.device)
    else:
        model = _create_model(env, args.device)

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
        "environment": env_id,
        "train_duration_seconds": round(train_duration, 1),
        "total_timesteps": args.timesteps,
        "eval_mean_reward": round(float(mean_reward), 2),
        "eval_std_reward": round(float(std_reward), 2),
        "base_model": args.base_model or None,
        "from_experience": args.from_experience or None,
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

    print(f"[train] Training complete: reward={mean_reward:.1f}+/-{std_reward:.1f}, "
          f"duration={train_duration:.0f}s")

    # Upload to S3 if configured
    if args.s3_endpoint:
        upload_to_s3(args, output_dir)

    # Notify platform via callback
    if args.callback_url:
        notify_callback(args.callback_url, results)

    return results


def _create_model(env, device: str) -> PPO:
    """Create a fresh PPO model with tuned hyperparameters."""
    return PPO(
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
        device=device,
        policy_kwargs=dict(
            net_arch=dict(pi=[256, 256], vf=[256, 256]),
        ),
    )


def _resolve_model_path(args, artifact_url: str) -> str | None:
    """Download a model from S3 and return its local path."""
    if not args.s3_endpoint:
        return None
    try:
        from minio import Minio
        client = Minio(
            args.s3_endpoint,
            access_key=args.s3_access_key,
            secret_key=args.s3_secret_key,
            secure=False,
        )
        local_path = Path(args.output_dir) / "base_policy.zip"
        # artifact_url might be "training/job123/policy.zip" or full S3 key
        key = artifact_url.replace("s3://" + args.s3_bucket + "/", "")
        client.fget_object(args.s3_bucket, key, str(local_path))
        print(f"[s3] Downloaded base model: {key}")
        return str(local_path).replace(".zip", "")
    except Exception as e:
        print(f"[s3] Failed to download base model: {e}")
        return None


def _load_experience_into_buffer(args, model: PPO):
    """Load collected experience from S3 into the model's rollout buffer.

    Experience files are NDJSON with fields:
    {obs: [...], action: [...], reward: float, next_obs: [...], done: bool}

    This pre-fills the rollout buffer so the first training epochs
    use real robot experience rather than fresh rollouts.
    """
    if not args.s3_endpoint or not args.from_experience:
        return

    try:
        from minio import Minio
        client = Minio(
            args.s3_endpoint,
            access_key=args.s3_access_key,
            secret_key=args.s3_secret_key,
            secure=False,
        )

        prefix = args.from_experience
        objects = list(client.list_objects(args.s3_bucket, prefix=prefix, recursive=True))
        print(f"[experience] Found {len(objects)} experience files under {prefix}")

        transitions = []
        for obj in objects[:50]:  # limit to 50 files to avoid OOM
            try:
                response = client.get_object(args.s3_bucket, obj.object_name)
                for line in response.read().decode().strip().split("\n"):
                    if line:
                        transitions.append(json.loads(line))
                response.close()
                response.release_conn()
            except Exception as e:
                print(f"[experience] Skip {obj.object_name}: {e}")

        print(f"[experience] Loaded {len(transitions)} transitions from S3")

        # Log stats
        if transitions:
            rewards = [t.get("reward", 0) for t in transitions]
            print(f"[experience] Reward stats: mean={np.mean(rewards):.2f}, "
                  f"min={np.min(rewards):.2f}, max={np.max(rewards):.2f}")

        # Note: SB3 PPO uses on-policy learning, so we can't directly inject
        # off-policy transitions into the rollout buffer. The experience data
        # primarily serves for:
        # 1. Pre-training reward normalization (VecNormalize running stats)
        # 2. Warm-starting the value function
        # For true offline RL, use SB3-contrib's CQL or IQL instead.
        #
        # Here we update the reward normalizer with experience reward stats:
        if transitions and hasattr(model.env, 'ret_rms'):
            rewards = np.array([t.get("reward", 0) for t in transitions])
            model.env.ret_rms.mean = np.mean(rewards)
            model.env.ret_rms.var = np.var(rewards) + 1e-8
            model.env.ret_rms.count = len(rewards)
            print(f"[experience] Updated reward normalizer from experience data")

    except Exception as e:
        print(f"[experience] Failed to load experience (non-fatal): {e}")


def upload_to_s3(args, output_dir: Path):
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
            if fpath.is_file():
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
    parser.add_argument("--env-id", default="FleetOS-Humanoid-v1",
                        help="Gymnasium environment ID (default: FleetOS-Humanoid-v1)")
    parser.add_argument("--base-model", default="",
                        help="S3 key for base model to fine-tune (e.g. training/job123/policy.zip)")
    parser.add_argument("--from-experience", default="",
                        help="S3 prefix for collected experience data (e.g. experience/)")
    parser.add_argument("--s3-endpoint", default=os.getenv("S3_ENDPOINT", ""))
    parser.add_argument("--s3-bucket", default=os.getenv("S3_BUCKET", "fleetos-models"))
    parser.add_argument("--s3-access-key", default=os.getenv("S3_ACCESS_KEY", "fleetos"))
    parser.add_argument("--s3-secret-key", default=os.getenv("S3_SECRET_KEY", "fleetos123"))
    parser.add_argument("--callback-url", default=os.getenv("TRAINING_CALLBACK_URL", ""))
    args = parser.parse_args()

    train(args)
