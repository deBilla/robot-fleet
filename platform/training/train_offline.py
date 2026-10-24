"""
Offline RL from fleet telemetry — no simulator in the training path.

Trains a SAC policy with a Conservative Q-Learning (CQL) penalty purely from
transitions the fleet recorded. The platform's processor writes them to the
telemetry bucket (training_data/.../transitions_<ns>.ndjson, one JSON per line:
robot_id, episode_id, step, obs[86], action[25], reward, episode_done, ...).
A dataset manifest (written by the training pipeline) pins exactly which files
a run uses, so every model can be traced back to its data.

Each record's reward is what its action earned until the robot's next record;
records are linked into (obs, action, reward, next_obs, done) within one
robot's episode by consecutive step index.

MuJoCo is used only when --eval-episodes > 0, as a "test track" that prints
the Katib objective (eval_mean_reward=<x>).

Usage:
    python train_offline.py --job-id JOB --dataset-manifest datasets/JOB/manifest.json \\
        --gradient-steps 20000 --s3-endpoint minio:9000
"""

import argparse
import json
import os
import sys
import time
from collections import defaultdict
from pathlib import Path

import gymnasium as gym
import numpy as np
from policy_action import RESIDUAL, from_joint_targets
from policy_meta import write_policy_meta
import torch
import torch.nn.functional as F
from stable_baselines3 import SAC
from stable_baselines3.common.vec_env import DummyVecEnv

# Policy contract shared with the simulator and inference service.
OBS_DIM = 86
ACT_DIM = 25
# Policies act in policy_action's RESIDUAL unit box, which is also SAC's own
# scaled action space; fleet joint targets are converted into it when loaded.
SCALED_ACTION_LOW, SCALED_ACTION_HIGH = -1.0, 1.0

ALGORITHM = "cql-sac"
GAMMA = 0.99
TAU = 0.005
NET_ARCH = [256, 256]
CQL_RANDOM_ACTIONS = 10


class OfflineSpaces(gym.Env):
    """Exposes the policy's observation/action spaces without any physics.

    SB3 needs an env to build a model; offline training never steps it.
    """

    def __init__(self):
        super().__init__()
        self.observation_space = gym.spaces.Box(-np.inf, np.inf, (OBS_DIM,), dtype=np.float32)
        self.action_space = gym.spaces.Box(SCALED_ACTION_LOW, SCALED_ACTION_HIGH, (ACT_DIM,), dtype=np.float32)

    def reset(self, *, seed=None, options=None):
        super().reset(seed=seed)
        return np.zeros(OBS_DIM, dtype=np.float32), {}

    def step(self, action):
        raise RuntimeError("OfflineSpaces cannot be stepped: training is offline")


def build_transitions(records: list[dict]) -> tuple[dict, dict]:
    """Link fleet records into transitions.

    Records are grouped by (robot_id, episode_id) and ordered by step. A record
    links to the record with the next consecutive step; a terminal record
    (episode_done) gets done=1 and next_obs=obs; anything else without a
    consecutive successor is dropped. Returns (arrays, stats).
    """
    stats = {"transitions": 0, "skipped_legacy": 0, "skipped_malformed": 0, "dropped_unlinked": 0}
    episodes: dict[tuple, dict[int, dict]] = defaultdict(dict)
    for r in records:
        if "obs" not in r:
            stats["skipped_legacy"] += 1
            continue
        if len(r.get("obs") or []) != OBS_DIM or len(r.get("action") or []) != ACT_DIM:
            stats["skipped_malformed"] += 1
            continue
        episodes[(r.get("robot_id"), r.get("episode_id"))][int(r.get("step", 0))] = r

    obs, actions, rewards, next_obs, dones = [], [], [], [], []
    for by_step in episodes.values():
        for step in sorted(by_step):
            r = by_step[step]
            terminal = bool(r.get("episode_done"))
            successor = by_step.get(step + 1)
            if not terminal and successor is None:
                stats["dropped_unlinked"] += 1
                continue
            obs.append(r["obs"])
            actions.append(r["action"])
            rewards.append(float(r.get("reward", 0.0)))
            next_obs.append(r["obs"] if terminal else successor["obs"])
            dones.append(1.0 if terminal else 0.0)

    stats["transitions"] = len(obs)
    data = {
        "observations": np.asarray(obs, dtype=np.float32).reshape(-1, OBS_DIM),
        "actions": np.asarray(actions, dtype=np.float32).reshape(-1, ACT_DIM),
        "rewards": np.asarray(rewards, dtype=np.float32),
        "next_observations": np.asarray(next_obs, dtype=np.float32).reshape(-1, OBS_DIM),
        "dones": np.asarray(dones, dtype=np.float32),
    }
    return data, stats


def _s3_client(args):
    from minio import Minio

    return Minio(args.s3_endpoint, access_key=args.s3_access_key,
                 secret_key=args.s3_secret_key, secure=False)


def _read_object(client, bucket: str, key: str) -> bytes:
    resp = client.get_object(bucket, key)
    try:
        return resp.read()
    finally:
        resp.close()
        resp.release_conn()


def load_dataset(args, client=None) -> tuple[list[dict], dict]:
    """Read the dataset manifest from the models bucket, then every listed file."""
    client = client or _s3_client(args)
    manifest = json.loads(_read_object(client, args.s3_bucket, args.dataset_manifest))
    records = []
    for key in manifest["keys"]:
        for line in _read_object(client, manifest["bucket"], key).decode().splitlines():
            if line.strip():
                records.append(json.loads(line))
    print(f"[offline] Loaded {len(records)} records from {len(manifest['keys'])} files "
          f"(manifest {args.dataset_manifest})")
    return records, manifest


def make_model(args, buffer_size: int) -> SAC:
    return SAC(
        "MlpPolicy",
        DummyVecEnv([OfflineSpaces]),
        learning_rate=args.learning_rate,
        buffer_size=max(buffer_size, 1),
        batch_size=args.batch_size,
        gamma=GAMMA,
        tau=TAU,
        verbose=0,
        device=args.device,
        policy_kwargs=dict(net_arch=NET_ARCH),
    )


def fill_replay_buffer(model: SAC, data: dict) -> int:
    """Load transitions into the model's ReplayBuffer (n_envs=1) in one copy.

    Fleet experience records the joint targets robots applied; the buffer
    holds the residual action that produces them (clipped to the residual range).
    """
    buffer = model.replay_buffer
    actions = from_joint_targets(data["actions"])
    n = len(data["rewards"])
    buffer.observations[:n, 0] = data["observations"]
    buffer.next_observations[:n, 0] = data["next_observations"]
    buffer.actions[:n, 0] = model.policy.scale_action(actions)
    buffer.rewards[:n, 0] = data["rewards"]
    buffer.dones[:n, 0] = data["dones"]
    buffer.pos = n % buffer.buffer_size
    buffer.full = n >= buffer.buffer_size
    return n


class CQLTrainer:
    """Conservative Q-Learning on top of SB3 SAC.

    Adds a CQL penalty to SAC's critic loss: Q-values on random
    (out-of-distribution) actions are pushed down relative to the actions in
    the data, so the policy doesn't exploit overestimated unseen actions.
    """

    def __init__(self, model: SAC, cql_alpha: float = 1.0):
        self.model = model
        self.cql_alpha = cql_alpha

    def train_offline(self, gradient_steps: int, batch_size: int = 256,
                      log_interval: int = 1000) -> dict:
        model = self.model
        ent_coef = model.ent_coef_tensor if hasattr(model, "ent_coef_tensor") and model.ent_coef_tensor is not None \
            else torch.exp(model.log_ent_coef.detach())
        losses = []

        for step in range(1, gradient_steps + 1):
            batch = model.replay_buffer.sample(batch_size)

            with torch.no_grad():
                next_actions, next_log_prob = model.actor.action_log_prob(batch.next_observations)
                next_log_prob = next_log_prob.reshape(-1, 1)
                q1_t, q2_t = model.critic_target(batch.next_observations, next_actions)
                target_q = torch.min(q1_t, q2_t) - ent_coef * next_log_prob
                target_q = batch.rewards + (1 - batch.dones) * GAMMA * target_q

            q1, q2 = model.critic(batch.observations, batch.actions)
            critic_loss = F.mse_loss(q1, target_q) + F.mse_loss(q2, target_q)

            obs = batch.observations
            random_actions = torch.empty(
                obs.shape[0] * CQL_RANDOM_ACTIONS, ACT_DIM, device=obs.device,
            ).uniform_(SCALED_ACTION_LOW, SCALED_ACTION_HIGH)
            obs_repeat = obs.repeat_interleave(CQL_RANDOM_ACTIONS, dim=0)
            rq1, rq2 = model.critic(obs_repeat, random_actions)
            cql_penalty = (
                torch.logsumexp(rq1.view(obs.shape[0], CQL_RANDOM_ACTIONS), dim=1).mean()
                + torch.logsumexp(rq2.view(obs.shape[0], CQL_RANDOM_ACTIONS), dim=1).mean()
                - q1.mean() - q2.mean()
            )
            loss = critic_loss + self.cql_alpha * cql_penalty
            model.critic.optimizer.zero_grad()
            loss.backward()
            model.critic.optimizer.step()

            actions_pi, log_prob = model.actor.action_log_prob(obs)
            q1_pi, q2_pi = model.critic(obs, actions_pi)
            actor_loss = (ent_coef * log_prob.reshape(-1, 1) - torch.min(q1_pi, q2_pi)).mean()
            model.actor.optimizer.zero_grad()
            actor_loss.backward()
            model.actor.optimizer.step()

            with torch.no_grad():
                for p, tp in zip(model.critic.parameters(), model.critic_target.parameters()):
                    tp.data.mul_(1 - TAU).add_(TAU * p.data)

            losses.append(float(loss.item()))
            if step % log_interval == 0:
                print(f"[cql] step={step}/{gradient_steps} loss={np.mean(losses[-log_interval:]):.4f} "
                      f"cql_penalty={float(cql_penalty):.4f}")

        return {"mean_loss": float(np.mean(losses)) if losses else 0.0}


def evaluate_on_test_track(model: SAC, env_id: str, episodes: int) -> float:
    """Run deterministic episodes in MuJoCo — the only simulator use here."""
    from stable_baselines3.common.evaluation import evaluate_policy

    from fleetos_env import register_fleetos_env

    register_fleetos_env()
    env = DummyVecEnv([lambda: gym.make(env_id)])
    try:
        mean_reward, _ = evaluate_policy(model, env, n_eval_episodes=episodes, deterministic=True)
    finally:
        env.close()
    return float(mean_reward)


def upload_artifacts(args, output_dir: Path):
    """Upload policy + results under training/<job-id>/. Raises on failure:
    the pipeline must not register a model whose artifact is missing."""
    client = _s3_client(args)
    if not client.bucket_exists(args.s3_bucket):
        client.make_bucket(args.s3_bucket)
    for fpath in sorted(output_dir.iterdir()):
        if fpath.is_file():
            key = f"training/{args.job_id}/{fpath.name}"
            client.fput_object(args.s3_bucket, key, str(fpath))
            print(f"[s3] Uploaded {key}")


def notify_callback(url: str, results: dict):
    """POST results to the platform callback endpoint (best-effort)."""
    try:
        import urllib.request

        req = urllib.request.Request(
            url, data=json.dumps(results).encode(),
            headers={"Content-Type": "application/json"}, method="POST",
        )
        urllib.request.urlopen(req, timeout=10)
        print(f"[callback] Notified {url}")
    except Exception as e:  # noqa: BLE001 — the platform also reads results from S3
        print(f"[callback] Failed (non-fatal): {e}")


def _finish(args, output_dir: Path, results: dict):
    with open(output_dir / "results.json", "w") as f:
        json.dump(results, f, indent=2)
    if args.no_upload:
        return
    if results["status"] == "completed" and args.s3_endpoint:
        upload_artifacts(args, output_dir)
    if args.callback_url:
        notify_callback(args.callback_url, results)


def run(args) -> int:
    """Train from the dataset manifest. Returns the process exit code."""
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    records, manifest = load_dataset(args)
    data, stats = build_transitions(records)
    n = stats["transitions"]
    print(f"offline_transitions={n}", flush=True)
    print(f"[offline] skipped_legacy={stats['skipped_legacy']} skipped_malformed={stats['skipped_malformed']} "
          f"dropped_unlinked={stats['dropped_unlinked']}")

    base = {
        "job_id": args.job_id,
        "algorithm": ALGORITHM,
        "dataset_manifest": args.dataset_manifest,
        "dataset_files": manifest.get("files"),
        "transitions": n,
        "skipped_legacy": stats["skipped_legacy"],
        "skipped_malformed": stats["skipped_malformed"],
    }
    if n < args.min_transitions:
        msg = f"insufficient fleet data: {n} transitions < --min-transitions {args.min_transitions}"
        print(f"[offline] ERROR: {msg}")
        _finish(args, output_dir, {**base, "status": "failed", "error_message": msg})
        return 1

    model = make_model(args, buffer_size=n)
    fill_replay_buffer(model, data)
    start = time.time()
    train_stats = CQLTrainer(model, cql_alpha=args.cql_alpha).train_offline(
        gradient_steps=args.gradient_steps, batch_size=args.batch_size, log_interval=args.log_interval,
    )
    duration = time.time() - start
    model.save(str(output_dir / "policy"))
    write_policy_meta(output_dir, RESIDUAL)  # obs + action version, checked before execution

    results = {
        **base,
        "status": "completed",
        "artifact_url": f"training/{args.job_id}/policy.zip",
        "train_duration_seconds": round(duration, 1),
        "mean_training_loss": round(train_stats["mean_loss"], 4),
        "hyperparameters": {
            "learning_rate": args.learning_rate,
            "batch_size": args.batch_size,
            "cql_alpha": args.cql_alpha,
            "gradient_steps": args.gradient_steps,
        },
    }
    if args.eval_episodes > 0:
        mean_reward = evaluate_on_test_track(model, args.env_id, args.eval_episodes)
        results["eval_mean_reward"] = round(mean_reward, 2)
        print(f"eval_mean_reward={mean_reward:.2f}", flush=True)  # Katib objective

    print(f"[offline] Trained on {n} transitions in {duration:.0f}s "
          f"(loss={train_stats['mean_loss']:.4f})")
    _finish(args, output_dir, results)
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Offline CQL training from fleet telemetry")
    p.add_argument("--job-id", required=True)
    p.add_argument("--dataset-manifest", required=True,
                   help="S3 key (in --s3-bucket) of the dataset manifest, e.g. datasets/<job>/manifest.json")
    p.add_argument("--output-dir", default="/tmp/training-output")
    p.add_argument("--gradient-steps", type=int, default=20_000)
    p.add_argument("--batch-size", type=int, default=256)
    p.add_argument("--learning-rate", type=float, default=3e-4)
    p.add_argument("--cql-alpha", type=float, default=1.0,
                   help="CQL conservatism (higher = stay closer to the fleet's actions)")
    p.add_argument("--min-transitions", type=int, default=1000)
    p.add_argument("--eval-episodes", type=int, default=0,
                   help="If > 0, evaluate on the MuJoCo test track and print eval_mean_reward")
    p.add_argument("--env-id", default="FleetOS-Humanoid-v2", help="Test-track env (eval only)")
    p.add_argument("--no-upload", action="store_true",
                   help="Skip artifact upload and callback (Katib trials only need the printed objective)")
    p.add_argument("--device", default="auto")
    p.add_argument("--log-interval", type=int, default=1000)
    p.add_argument("--s3-endpoint", default=os.getenv("S3_ENDPOINT", ""))
    p.add_argument("--s3-bucket", default=os.getenv("S3_BUCKET", "fleetos-models"))
    p.add_argument("--s3-access-key", default=os.getenv("S3_ACCESS_KEY", "fleetos"))
    p.add_argument("--s3-secret-key", default=os.getenv("S3_SECRET_KEY", "fleetos123"))
    p.add_argument("--callback-url", default=os.getenv("TRAINING_CALLBACK_URL", ""))
    return p


if __name__ == "__main__":
    sys.exit(run(build_parser().parse_args()))
