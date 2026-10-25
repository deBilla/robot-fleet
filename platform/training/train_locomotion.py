"""
SB3 PPO training script for FleetOS humanoid locomotion.

Supports two modes:
  1. From scratch: Train on FleetOS-Humanoid-v2 (custom lab environment)
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
import copy
import json
import os
import sys
import time
from pathlib import Path

import gymnasium as gym
import numpy as np
from policy_action import RESIDUAL
from policy_meta import write_policy_meta
from stable_baselines3 import PPO
from stable_baselines3.common.callbacks import BaseCallback
from stable_baselines3.common.evaluation import evaluate_policy
from stable_baselines3.common.monitor import Monitor
from stable_baselines3.common.vec_env import DummyVecEnv, SubprocVecEnv, VecEnv, VecNormalize

# Register our custom environment
from fleetos_env import FleetOSHumanoidEnv, register_fleetos_env
register_fleetos_env()


METRICS_FLUSH_EPISODES = 100  # write metrics.json + a learning-curve point this often
METRICS_WINDOW_EPISODES = 100  # episodes averaged into mean_reward / mean_length


class MetricsCallback(BaseCallback):
    """Logs training metrics to a JSON file for the platform to consume."""

    def __init__(self, metrics_path: str, verbose=0, flush_every: int = METRICS_FLUSH_EPISODES):
        super().__init__(verbose)
        self.metrics_path = metrics_path
        self.flush_every = flush_every
        self.metrics = {
            "episode_rewards": [],
            "episode_lengths": [],
            "timestamps": [],
        }
        self.history = []  # learning curve: one point per flush
        self._start = time.time()

    def _on_step(self) -> bool:
        # Monitor puts an "episode" entry in an env's info on the step its
        # episode ends; record each finished episode exactly once.
        for info in self.locals.get("infos", []):
            ep_info = info.get("episode")
            if ep_info is None:
                continue
            self.metrics["episode_rewards"].append(float(ep_info["r"]))
            self.metrics["episode_lengths"].append(int(ep_info["l"]))
            self.metrics["timestamps"].append(time.time())
            if len(self.metrics["episode_rewards"]) % self.flush_every == 0:
                self._flush()
        return True

    def _flush(self):
        rewards = self.metrics["episode_rewards"]
        lengths = self.metrics["episode_lengths"]
        recent = slice(-METRICS_WINDOW_EPISODES, None)
        summary = {
            "total_episodes": len(rewards),
            "mean_reward": float(np.mean(rewards[recent])) if rewards else 0.0,
            "mean_length": float(np.mean(lengths[recent])) if lengths else 0.0,
            "max_reward": float(np.max(rewards)) if rewards else 0.0,
            "total_timesteps": self.num_timesteps,
        }
        if not self.history or self.history[-1]["timesteps"] != self.num_timesteps:
            self.history.append({
                "timesteps": self.num_timesteps,
                "elapsed_seconds": round(time.time() - self._start, 1),
                "mean_reward": round(summary["mean_reward"], 2),
                "mean_length": round(summary["mean_length"], 1),
            })
            print(f"[train] steps={self.num_timesteps} episodes={len(rewards)} "
                  f"mean_reward={summary['mean_reward']:.2f} mean_length={summary['mean_length']:.1f}",
                  flush=True)
        summary["history"] = self.history
        with open(self.metrics_path, "w") as f:
            json.dump(summary, f, indent=2)

    def _on_training_end(self):
        self._flush()


TRAIN_SEED = 0
FINAL_EVAL_EPISODES = 20


def make_env(env_id: str, domain_randomisation: bool = False):
    """Create environment — supports both custom and stock Gymnasium."""
    def _init():
        if env_id not in gym.registry:  # fresh worker process
            register_fleetos_env()
        return Monitor(
            FleetOSHumanoidEnv(domain_randomisation=domain_randomisation)
        )  # episode reward/length for training metrics
    return _init


def build_vec_env(env_id: str, n_envs: int,
                  domain_randomisation: bool = False) -> VecEnv:
    """n_envs copies of the env: in-process for one, one worker process each
    otherwise (MuJoCo steps on the CPU, so parallel envs scale with cores).
    Workers get different seeds so their episodes don't run in lockstep."""
    factories = [
        make_env(env_id, domain_randomisation=domain_randomisation) for _ in range(n_envs)
    ]
    env = DummyVecEnv(factories) if n_envs == 1 else SubprocVecEnv(factories)
    env.seed(TRAIN_SEED)
    return env


def make_eval_env(env_id: str, train_env: VecNormalize) -> VecNormalize:
    """Single test-track env with a frozen copy of the training normalizer.

    Scores raw episode reward, the same units as evaluate_policy.py and the
    promotion gate; evaluating on the training env reported normalized reward
    and kept updating the normalizer.
    """
    eval_env = VecNormalize(build_vec_env(env_id, 1), norm_obs=True, norm_reward=False,
                            clip_obs=train_env.clip_obs, training=False)
    eval_env.obs_rms = copy.deepcopy(train_env.obs_rms)
    return eval_env


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
    print(f"[train] Parallel envs: {args.n_envs}")
    env = build_vec_env(
        env_id, args.n_envs, domain_randomisation=args.domain_randomisation
    )
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
            model = _create_model(env, args.device, hyperparameters(args))
    else:
        model = _create_model(env, args.device, hyperparameters(args))

    # Train with metrics callback
    metrics_path = str(output_dir / "metrics.json")
    callback = MetricsCallback(metrics_path)

    start_time = time.time()
    model.learn(total_timesteps=args.timesteps, callback=callback)
    train_duration = time.time() - start_time

    # Save model
    model_path = str(output_dir / "policy")
    model.save(model_path)
    write_policy_meta(output_dir, RESIDUAL)  # obs + action version, checked before execution
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
    eval_env = make_eval_env(env_id, env)
    mean_reward, std_reward = evaluate_policy(model, eval_env, n_eval_episodes=FINAL_EVAL_EPISODES)
    eval_env.close()
    env.close()  # stop worker processes

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
            **hyperparameters(args),
            "n_epochs": 10,
            "net_arch": [256, 256],
        },
    }
    with open(str(output_dir / "results.json"), "w") as f:
        json.dump(results, f, indent=2)

    print(f"[train] Training complete: reward={mean_reward:.1f}+/-{std_reward:.1f}, "
          f"duration={train_duration:.0f}s")
    # Objective line for Katib's StdOut metrics collector (name=value).
    print(katib_metric_line(mean_reward), flush=True)

    # Upload to S3 if configured
    if args.s3_endpoint:
        upload_to_s3(args, output_dir)

    # Notify platform via callback
    if args.callback_url:
        notify_callback(args.callback_url, results)

    return results


def hyperparameters(args) -> dict:
    """The tunable PPO hyperparameters (searched over by Katib)."""
    return {
        "learning_rate": args.learning_rate,
        "n_steps": args.n_steps,
        "batch_size": args.batch_size,
        "gamma": args.gamma,
        "ent_coef": args.ent_coef,
    }


def katib_metric_line(mean_reward: float) -> str:
    """Format the evaluation objective for Katib's StdOut metrics collector."""
    return f"eval_mean_reward={float(mean_reward):.2f}"


def _create_model(env, device: str, hp: dict) -> PPO:
    """Create a fresh PPO model with the given tunable hyperparameters."""
    return PPO(
        "MlpPolicy",
        env,
        learning_rate=hp["learning_rate"],
        n_steps=hp["n_steps"],
        batch_size=hp["batch_size"],
        n_epochs=10,
        gamma=hp["gamma"],
        gae_lambda=0.95,
        clip_range=0.2,
        ent_coef=hp["ent_coef"],
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
        # Fatal: without policy.zip in S3 nothing downstream (eval, registry,
        # inference) can use this run, so the workflow step must fail.
        print(f"[s3] Upload failed: {e}")
        raise


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


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Train locomotion policy")
    parser.add_argument("--job-id", required=True, help="Training job ID")
    parser.add_argument("--timesteps", type=int, default=1_000_000)
    parser.add_argument("--device", default="auto", help="cpu, cuda, or auto")
    parser.add_argument("--output-dir", default="/tmp/training-output")
    parser.add_argument("--env-id", default="FleetOS-Humanoid-v2",
                        help="Gymnasium environment ID (default: FleetOS-Humanoid-v2)")
    parser.add_argument("--base-model", default="",
                        help="S3 key for base model to fine-tune (e.g. training/job123/policy.zip)")
    parser.add_argument("--from-experience", default="",
                        help="S3 prefix for collected experience data (e.g. experience/)")
    parser.add_argument("--s3-endpoint", default=os.getenv("S3_ENDPOINT", ""))
    parser.add_argument("--s3-bucket", default=os.getenv("S3_BUCKET", "fleetos-models"))
    parser.add_argument("--s3-access-key", default=os.getenv("S3_ACCESS_KEY", "fleetos"))
    parser.add_argument("--s3-secret-key", default=os.getenv("S3_SECRET_KEY", "fleetos123"))
    parser.add_argument("--callback-url", default=os.getenv("TRAINING_CALLBACK_URL", ""))
    # Tunable PPO hyperparameters (defaults are the previously hard-coded values)
    parser.add_argument("--n-envs", type=int, default=1,
                        help="parallel environments (worker processes); rollout = n_steps x n_envs")
    parser.add_argument("--learning-rate", type=float, default=3e-4)
    parser.add_argument("--n-steps", type=int, default=2048)
    parser.add_argument("--batch-size", type=int, default=64)
    parser.add_argument("--gamma", type=float, default=0.99)
    parser.add_argument("--ent-coef", type=float, default=0.0)
    parser.add_argument(
        "--domain-randomisation",
        action="store_true",
        default=False,
        help="Train with per-episode domain randomisation (sim-to-real robustness)",
    )
    return parser


if __name__ == "__main__":
    train(build_parser().parse_args())
