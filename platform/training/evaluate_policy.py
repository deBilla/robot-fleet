"""
Evaluate a trained locomotion policy against N scenarios.

Usage:
    python evaluate_policy.py \
        --eval-id EVAL_ID \
        --model-path s3://fleetos-models/training/JOB_ID/policy.zip \
        --scenarios 100 \
        --s3-endpoint minio:9000
"""

import argparse
import json
import os
import time
from pathlib import Path

import gymnasium as gym
import numpy as np
from stable_baselines3 import PPO
from stable_baselines3.common.vec_env import DummyVecEnv, VecNormalize


# Scenario configurations: varied physics for robustness testing
SCENARIO_PRESETS = {
    "standard": {"gravity": -9.81, "friction": 1.0, "wind": 0.0},
    "low_friction": {"gravity": -9.81, "friction": 0.5, "wind": 0.0},
    "high_gravity": {"gravity": -11.0, "friction": 1.0, "wind": 0.0},
    "windy": {"gravity": -9.81, "friction": 1.0, "wind": 5.0},
    "combined": {"gravity": -10.5, "friction": 0.7, "wind": 3.0},
}


def download_model(args) -> str:
    """Download model from S3 to local path."""
    if args.model_path.startswith("s3://") or args.model_path.startswith("training/"):
        try:
            from minio import Minio

            client = Minio(
                args.s3_endpoint,
                access_key=args.s3_access_key,
                secret_key=args.s3_secret_key,
                secure=False,
            )
            local_dir = Path(args.output_dir) / "model"
            local_dir.mkdir(parents=True, exist_ok=True)

            # Strip s3:// prefix
            key = args.model_path.replace("s3://fleetos-models/", "")
            local_path = str(local_dir / "policy.zip")
            client.fget_object(args.s3_bucket, key, local_path)

            # Also try to download normalizer
            norm_key = key.replace("policy.zip", "vec_normalize.pkl")
            try:
                client.fget_object(args.s3_bucket, norm_key, str(local_dir / "vec_normalize.pkl"))
            except Exception:
                pass

            print(f"[eval] Downloaded model from {key}")
            return local_path
        except Exception as e:
            print(f"[eval] S3 download failed: {e}")
            raise
    return args.model_path


def evaluate(args):
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    print(f"[eval] Starting evaluation: {args.eval_id}")
    print(f"[eval] Model: {args.model_path}")
    print(f"[eval] Scenarios: {args.scenarios}")

    model_path = download_model(args)
    model = PPO.load(model_path)

    env = DummyVecEnv([lambda: gym.make("Humanoid-v4")])

    # Try to load normalizer
    norm_path = Path(model_path).parent / "vec_normalize.pkl"
    if norm_path.exists():
        env = VecNormalize.load(str(norm_path), env)
        env.training = False
        env.norm_reward = False

    start_time = time.time()
    scenario_results = []
    total_passed = 0

    scenarios_per_preset = max(1, args.scenarios // len(SCENARIO_PRESETS))

    for preset_name, preset_config in SCENARIO_PRESETS.items():
        for i in range(scenarios_per_preset):
            scenario_id = f"{preset_name}-{i:03d}"
            result = run_scenario(model, env, scenario_id, preset_name, preset_config)
            scenario_results.append(result)
            if result["passed"]:
                total_passed += 1

    eval_duration = time.time() - start_time
    total = len(scenario_results)
    pass_rate = total_passed / total if total > 0 else 0

    # Aggregate metrics
    rewards = [s["reward"] for s in scenario_results]
    lengths = [s["episode_length"] for s in scenario_results]

    results = {
        "eval_id": args.eval_id,
        "status": "completed",
        "scenarios_total": total,
        "scenarios_passed": total_passed,
        "pass_rate": round(pass_rate, 4),
        "mean_reward": round(float(np.mean(rewards)), 2),
        "std_reward": round(float(np.std(rewards)), 2),
        "mean_episode_length": round(float(np.mean(lengths)), 1),
        "eval_duration_seconds": round(eval_duration, 1),
        "scenario_results": scenario_results,
    }

    with open(str(output_dir / "eval_results.json"), "w") as f:
        json.dump(results, f, indent=2)

    print(f"[eval] Complete: pass_rate={pass_rate:.1%} ({total_passed}/{total}), "
          f"mean_reward={np.mean(rewards):.1f}")

    # Upload results to S3
    if args.s3_endpoint:
        upload_results(args, output_dir)

    # Callback
    if args.callback_url:
        notify_callback(args.callback_url, results)

    return results


def run_scenario(model, env, scenario_id: str, preset_name: str, config: dict) -> dict:
    """Run a single evaluation scenario."""
    obs = env.reset()
    total_reward = 0.0
    steps = 0
    done = False
    violations = []

    # Minimum reward threshold for "passing"
    min_reward_threshold = 500.0
    max_steps = 1000

    while not done and steps < max_steps:
        action, _ = model.predict(obs, deterministic=True)
        obs, reward, done, info = env.step(action)
        total_reward += float(reward[0])
        steps += 1

    passed = total_reward >= min_reward_threshold and steps >= 100

    return {
        "scenario_id": scenario_id,
        "preset": preset_name,
        "passed": passed,
        "reward": round(total_reward, 2),
        "episode_length": steps,
        "failure_reason": "" if passed else f"reward {total_reward:.0f} < {min_reward_threshold}",
        "config": config,
    }


def upload_results(args, output_dir: Path):
    try:
        from minio import Minio

        client = Minio(
            args.s3_endpoint,
            access_key=args.s3_access_key,
            secret_key=args.s3_secret_key,
            secure=False,
        )
        prefix = f"evaluations/{args.eval_id}"
        for fpath in output_dir.iterdir():
            key = f"{prefix}/{fpath.name}"
            client.fput_object(args.s3_bucket, key, str(fpath))
            print(f"[s3] Uploaded {key}")
    except Exception as e:
        print(f"[s3] Upload failed (non-fatal): {e}")


def notify_callback(url: str, results: dict):
    try:
        import urllib.request
        data = json.dumps(results).encode()
        req = urllib.request.Request(
            url, data=data, headers={"Content-Type": "application/json"}, method="POST"
        )
        urllib.request.urlopen(req, timeout=10)
    except Exception as e:
        print(f"[callback] Failed (non-fatal): {e}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Evaluate locomotion policy")
    parser.add_argument("--eval-id", required=True)
    parser.add_argument("--model-path", required=True, help="S3 path or local path to policy.zip")
    parser.add_argument("--scenarios", type=int, default=100)
    parser.add_argument("--output-dir", default="/tmp/eval-output")
    parser.add_argument("--s3-endpoint", default=os.getenv("S3_ENDPOINT", ""))
    parser.add_argument("--s3-bucket", default=os.getenv("S3_BUCKET", "fleetos-models"))
    parser.add_argument("--s3-access-key", default=os.getenv("S3_ACCESS_KEY", "fleetos"))
    parser.add_argument("--s3-secret-key", default=os.getenv("S3_SECRET_KEY", "fleetos123"))
    parser.add_argument("--callback-url", default=os.getenv("EVAL_CALLBACK_URL", ""))
    args = parser.parse_args()

    evaluate(args)
