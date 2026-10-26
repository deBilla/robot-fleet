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
from policy_meta import META_FILENAME, action_version, incompatibility, read_policy_meta
from stable_baselines3 import PPO, SAC
from stable_baselines3.common.vec_env import DummyVecEnv, VecNormalize

# Online (simulator) runs produce PPO; offline fleet-data runs produce SAC (CQL).
POLICY_LOADERS = (("PPO", PPO), ("SAC", SAC))


def load_policy(model_path: str):
    """Load a policy of any supported algorithm. Returns (model, algorithm)."""
    errors = []
    for name, cls in POLICY_LOADERS:
        try:
            return cls.load(model_path), name
        except Exception as e:  # noqa: BLE001 — try the next algorithm
            errors.append(f"{name}: {e}")
    raise RuntimeError(f"cannot load policy {model_path}: " + "; ".join(errors))


# Scenario configurations: varied physics for robustness testing
SCENARIO_PRESETS = {
    "standard": {"gravity": -9.81, "friction": 1.0, "wind": 0.0},
    "low_friction": {"gravity": -9.81, "friction": 0.5, "wind": 0.0},
    "high_gravity": {"gravity": -11.0, "friction": 1.0, "wind": 0.0},
    "windy": {"gravity": -9.81, "friction": 1.0, "wind": 5.0},
    "combined": {"gravity": -10.5, "friction": 0.7, "wind": 3.0},
}


class IncompatiblePolicy(RuntimeError):
    """The policy was trained on a different observation layout."""


def check_compatible(model_path: str) -> None:
    """Refuse policies whose policy_meta.json doesn't match this build."""
    reason = incompatibility(read_policy_meta(Path(model_path).parent / META_FILENAME))
    if reason:
        raise IncompatiblePolicy(f"{model_path}: {reason}")


def make_test_track(model_path: str, env_id: str,
                    domain_randomisation: bool = False) -> DummyVecEnv:
    """The test track, mapping actions to joints the way this policy was trained to."""
    from fleetos_env import register_fleetos_env
    register_fleetos_env()
    version = action_version(read_policy_meta(Path(model_path).parent / META_FILENAME))
    return DummyVecEnv(
        [lambda: gym.make(env_id, action_version=version,
                          domain_randomisation=domain_randomisation)]
    )


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

            # Strip s3://<bucket>/ prefix
            key = args.model_path.replace(f"s3://{args.s3_bucket}/", "")
            local_path = str(local_dir / "policy.zip")
            client.fget_object(args.s3_bucket, key, local_path)

            # Optional siblings: normalizer (PPO) and compatibility metadata
            for sibling in ("vec_normalize.pkl", META_FILENAME):
                try:
                    client.fget_object(args.s3_bucket, key.replace("policy.zip", sibling), str(local_dir / sibling))
                except Exception:  # noqa: BLE001 — absent siblings are handled by the caller
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
    check_compatible(model_path)
    model, algorithm = load_policy(model_path)
    print(f"[eval] Loaded {algorithm} policy")

    env = make_test_track(model_path, args.env_id,
                          domain_randomisation=args.domain_randomisation)

    # Online PPO runs ship a normalizer; offline models train on raw
    # observations (like inference) and have none.
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
        "domain_randomisation": args.domain_randomisation,
    }
    # Summary map for the platform's eval callback (EvalCallbackPayload.results).
    results["results"] = {
        "mean_reward": results["mean_reward"],
        "std_reward": results["std_reward"],
        "mean_episode_length": results["mean_episode_length"],
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
    """Run a single evaluation scenario under its preset's physics."""
    env.env_method("set_conditions", **config)
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
            if not fpath.is_file():
                continue  # skip the downloaded model/ directory
            key = f"{prefix}/{fpath.name}"
            client.fput_object(args.s3_bucket, key, str(fpath))
            print(f"[s3] Uploaded {key}")
    except Exception as e:
        # Fatal: the platform's evaluation gate reads eval_results.json from S3.
        print(f"[s3] Upload failed: {e}")
        raise


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
    parser.add_argument("--env-id", default="FleetOS-Humanoid-v2", help="Gymnasium environment ID")
    parser.add_argument("--scenarios", type=int, default=100)
    parser.add_argument("--output-dir", default="/tmp/eval-output")
    parser.add_argument("--s3-endpoint", default=os.getenv("S3_ENDPOINT", ""))
    parser.add_argument("--s3-bucket", default=os.getenv("S3_BUCKET", "fleetos-models"))
    parser.add_argument("--s3-access-key", default=os.getenv("S3_ACCESS_KEY", "fleetos"))
    parser.add_argument("--s3-secret-key", default=os.getenv("S3_SECRET_KEY", "fleetos123"))
    parser.add_argument("--callback-url", default=os.getenv("EVAL_CALLBACK_URL", ""))
    parser.add_argument(
        "--domain-randomisation",
        action="store_true",
        default=False,
        help="Evaluate with per-episode domain randomisation on (measures sim-to-real robustness)",
    )
    args = parser.parse_args()

    evaluate(args)
