"""
Kubeflow Pipeline definition for FleetOS locomotion training.

Defines a two-stage pipeline:
1. Train a PPO locomotion policy on Humanoid-v4
2. Evaluate the trained policy against N scenarios

Usage:
    # Compile to YAML for Kubeflow
    python pipeline.py --compile

    # Submit directly to Kubeflow
    python pipeline.py --submit --job-id JOB_ID --timesteps 2000000
"""

import argparse

from kfp import dsl, compiler
from kfp.dsl import Input, Output, Artifact, Metrics


TRAINING_IMAGE = "fleetos/training:latest"


@dsl.component(base_image=TRAINING_IMAGE)
def train_locomotion(
    job_id: str,
    timesteps: int,
    device: str,
    s3_endpoint: str,
    s3_bucket: str,
    s3_access_key: str,
    s3_secret_key: str,
    callback_url: str,
    model_artifact: Output[Artifact],
    training_metrics: Output[Metrics],
):
    """Train PPO locomotion policy on Humanoid-v4."""
    import json
    import subprocess

    result = subprocess.run(
        [
            "python", "/app/train_locomotion.py",
            "--job-id", job_id,
            "--timesteps", str(timesteps),
            "--device", device,
            "--output-dir", "/tmp/training-output",
            "--s3-endpoint", s3_endpoint,
            "--s3-bucket", s3_bucket,
            "--s3-access-key", s3_access_key,
            "--s3-secret-key", s3_secret_key,
            "--callback-url", callback_url,
        ],
        capture_output=True, text=True,
    )
    print(result.stdout)
    if result.returncode != 0:
        print(result.stderr)
        raise RuntimeError(f"Training failed: {result.stderr}")

    # Read metrics for KFP tracking
    with open("/tmp/training-output/metrics.json") as f:
        metrics = json.load(f)

    training_metrics.log_metric("mean_reward", metrics.get("mean_reward", 0))
    training_metrics.log_metric("total_episodes", metrics.get("total_episodes", 0))
    training_metrics.log_metric("total_timesteps", metrics.get("total_timesteps", 0))

    model_artifact.path = "/tmp/training-output/policy.zip"
    model_artifact.metadata["job_id"] = job_id
    model_artifact.metadata["s3_path"] = f"training/{job_id}/policy.zip"


@dsl.component(base_image=TRAINING_IMAGE)
def evaluate_locomotion(
    eval_id: str,
    job_id: str,
    scenarios: int,
    s3_endpoint: str,
    s3_bucket: str,
    s3_access_key: str,
    s3_secret_key: str,
    callback_url: str,
    eval_metrics: Output[Metrics],
):
    """Evaluate trained policy against N scenarios."""
    import json
    import subprocess

    model_path = f"training/{job_id}/policy.zip"

    result = subprocess.run(
        [
            "python", "/app/evaluate_policy.py",
            "--eval-id", eval_id,
            "--model-path", model_path,
            "--scenarios", str(scenarios),
            "--output-dir", "/tmp/eval-output",
            "--s3-endpoint", s3_endpoint,
            "--s3-bucket", s3_bucket,
            "--s3-access-key", s3_access_key,
            "--s3-secret-key", s3_secret_key,
            "--callback-url", callback_url,
        ],
        capture_output=True, text=True,
    )
    print(result.stdout)
    if result.returncode != 0:
        print(result.stderr)
        raise RuntimeError(f"Evaluation failed: {result.stderr}")

    with open("/tmp/eval-output/eval_results.json") as f:
        results = json.load(f)

    eval_metrics.log_metric("pass_rate", results.get("pass_rate", 0))
    eval_metrics.log_metric("mean_reward", results.get("mean_reward", 0))
    eval_metrics.log_metric("scenarios_passed", results.get("scenarios_passed", 0))
    eval_metrics.log_metric("scenarios_total", results.get("scenarios_total", 0))


@dsl.pipeline(
    name="FleetOS Locomotion Training",
    description="Train and evaluate a PPO locomotion policy for humanoid robots.",
)
def locomotion_training_pipeline(
    job_id: str,
    timesteps: int = 1_000_000,
    eval_scenarios: int = 100,
    device: str = "auto",
    s3_endpoint: str = "minio:9000",
    s3_bucket: str = "fleetos-models",
    s3_access_key: str = "fleetos",
    s3_secret_key: str = "fleetos123",
    train_callback_url: str = "",
    eval_callback_url: str = "",
):
    train_task = train_locomotion(
        job_id=job_id,
        timesteps=timesteps,
        device=device,
        s3_endpoint=s3_endpoint,
        s3_bucket=s3_bucket,
        s3_access_key=s3_access_key,
        s3_secret_key=s3_secret_key,
        callback_url=train_callback_url,
    )

    # GPU resource request (optional, falls back to CPU)
    train_task.set_cpu_limit("4")
    train_task.set_memory_limit("8Gi")

    eval_task = evaluate_locomotion(
        eval_id=f"{job_id}-eval",
        job_id=job_id,
        scenarios=eval_scenarios,
        s3_endpoint=s3_endpoint,
        s3_bucket=s3_bucket,
        s3_access_key=s3_access_key,
        s3_secret_key=s3_secret_key,
        callback_url=eval_callback_url,
    )
    eval_task.after(train_task)
    eval_task.set_cpu_limit("2")
    eval_task.set_memory_limit("4Gi")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--compile", action="store_true", help="Compile pipeline to YAML")
    parser.add_argument("--output", default="locomotion_pipeline.yaml")
    args = parser.parse_args()

    if args.compile:
        compiler.Compiler().compile(
            pipeline_func=locomotion_training_pipeline,
            package_path=args.output,
        )
        print(f"Pipeline compiled to {args.output}")
