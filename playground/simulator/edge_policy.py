"""
Edge policy execution: each robot runs its assigned low-level policy locally,
every control tick, on the current observation — the same way policies are
trained and evaluated (an action every 7 × 0.003 s physics substeps).

The platform still decides *which* model each robot runs (canary assignment /
fleet default) via GET /api/v1/robots/{id}/policy; the robot only executes it.
Commands that aren't locomotion (gestures, NL instructions) keep using the
cloud inference service.

Environment variables:
    EDGE_POLICY          "true" (default) to run assigned policies locally
    EDGE_POLICY_TASKS    tasks served locally (default "walk,move,move_relative")
    POLICY_REFRESH_S     seconds between assignment checks (default 10)
    PLATFORM_API_URL     platform API (default http://api:8080)
    PLATFORM_API_KEY     API key for the policy endpoint (default dev-key-002)
    S3_ENDPOINT          MinIO/S3 endpoint holding model artifacts (default minio:9000)
    S3_ACCESS_KEY        (default fleetos)
    S3_SECRET_KEY        (default fleetos123)
    MODELS_BUCKET        bucket with training/<job>/policy.zip (default fleetos-models)
    POLICY_CACHE_DIR     local artifact cache (default /tmp/fleetos-edge-policies)
"""

import hashlib
import logging
import os
import pickle
import threading
from pathlib import Path
from typing import Callable, Optional

import numpy as np

from policy_action import ABSOLUTE, to_joint_targets
from policy_meta import META_FILENAME, action_version, incompatibility, read_policy_meta

log = logging.getLogger(__name__)

OBS_DIM = 86
ACTION_DIM = 25
HTTP_TIMEOUT_S = 5.0
NORMALIZER_FILE = "vec_normalize.pkl"
DEFAULT_TASKS = "walk,move,move_relative"


# ─── Pure helpers ───────────────────────────────────────────────

def parse_tasks(spec: str) -> frozenset:
    return frozenset(t.strip() for t in spec.split(",") if t.strip())


def choose_action_source(task: str, edge_enabled: bool, edge_action, edge_tasks: frozenset) -> str:
    """'edge' when this tick's action should come from the local policy."""
    if edge_enabled and task in edge_tasks and edge_action is not None:
        return "edge"
    return "cloud"


def s3_key(artifact_url: str) -> str:
    """Bare S3 key from 's3://bucket/key' or 'key'."""
    if artifact_url.startswith("s3://"):
        return artifact_url[5:].partition("/")[2]
    return artifact_url


def normalizer_key(artifact_url: str) -> str:
    """S3 key of the vec_normalize.pkl uploaded next to an artifact."""
    return str(Path(s3_key(artifact_url)).parent / NORMALIZER_FILE)


def normalize_obs(obs, mean, var, clip_obs: float, epsilon: float) -> np.ndarray:
    """VecNormalize's observation transform: clip((obs - mean) / sqrt(var + eps))."""
    out = (np.asarray(obs, dtype=np.float64) - mean) / np.sqrt(var + epsilon)
    return np.clip(out, -clip_obs, clip_obs).astype(np.float32)


def load_obs_normalizer(path: str) -> Optional[Callable]:
    """obs -> normalized obs from a saved VecNormalize, or None if absent."""
    if not os.path.exists(path):
        return None
    with open(path, "rb") as f:
        vec = pickle.load(f)  # trusted: written by FleetOS training jobs
    mean, var = np.asarray(vec.obs_rms.mean), np.asarray(vec.obs_rms.var)
    clip_obs, epsilon = float(vec.clip_obs), float(vec.epsilon)
    return lambda obs: normalize_obs(obs, mean, var, clip_obs, epsilon)


class NormalizedPolicy:
    """Wraps a policy so predict() sees training-scaled observations."""

    def __init__(self, policy, normalize: Callable):
        self._policy = policy
        self._normalize = normalize

    def predict(self, obs, deterministic=False):
        return self._policy.predict(self._normalize(obs), deterministic=deterministic)


class IncompatiblePolicy(RuntimeError):
    """The policy was trained on another observation layout or maps actions in an unknown way; never execute it."""


class JointTargetPolicy:
    """Wraps a policy so predict() returns joint position targets (policy_action.py)."""

    def __init__(self, policy, action_version: int):
        self._policy = policy
        self._action_version = action_version

    def predict(self, obs, deterministic=False):
        action, state = self._policy.predict(obs, deterministic=deterministic)
        return to_joint_targets(action, self._action_version), state


def load_policy_file(policy_path: str):
    """Load an SB3 policy (PPO, then SAC, then TQC) + its normalizer if present.

    Refuses policies whose policy_meta.json doesn't match this build's
    observation version (the robot then stays on cloud inference). The loaded
    policy returns joint targets whatever its action version.
    """
    meta = read_policy_meta(Path(policy_path).parent / META_FILENAME)
    reason = incompatibility(meta)
    if reason:
        raise IncompatiblePolicy(f"{policy_path}: {reason}")

    policy = _load_sb3(policy_path)
    normalize = load_obs_normalizer(str(Path(policy_path).parent / NORMALIZER_FILE))
    if normalize:
        policy = NormalizedPolicy(policy, normalize)
    version = action_version(meta)
    return policy if version == ABSOLUTE else JointTargetPolicy(policy, version)


def _load_sb3(policy_path: str):
    from stable_baselines3 import PPO, SAC

    loaders = [PPO.load, SAC.load]
    try:
        from sb3_contrib import TQC
        loaders.append(TQC.load)
    except ImportError:
        pass
    errors = []
    for load in loaders:
        try:
            return load(policy_path)
        except Exception as exc:  # try the next algorithm
            errors.append(str(exc))
    raise RuntimeError(f"load policy {policy_path}: {'; '.join(errors)}")


def make_s3_fetch_and_load(endpoint: str, access_key: str, secret_key: str,
                           bucket: str, cache_dir: str) -> Callable:
    """artifact_url -> loaded policy, downloading policy.zip (+ normalizer) once."""

    def fetch_and_load(artifact_url: str):
        from minio import Minio

        client = Minio(endpoint, access_key=access_key, secret_key=secret_key, secure=False)
        digest = hashlib.sha1(artifact_url.encode()).hexdigest()[:16]
        local_dir = Path(cache_dir) / digest
        local_dir.mkdir(parents=True, exist_ok=True)
        zip_path = local_dir / "policy.zip"
        if not zip_path.exists():
            client.fget_object(bucket, s3_key(artifact_url), str(zip_path))
            try:
                client.fget_object(bucket, normalizer_key(artifact_url), str(local_dir / NORMALIZER_FILE))
            except Exception as exc:  # optional: offline models have no normalizer
                log.info("no normalizer for %s (%s): raw observations", artifact_url, exc)
            try:
                meta_key = s3_key(artifact_url).replace("policy.zip", META_FILENAME)
                client.fget_object(bucket, meta_key, str(local_dir / META_FILENAME))
            except Exception as exc:  # missing metadata is refused by load_policy_file
                log.warning("no %s for %s (%s)", META_FILENAME, artifact_url, exc)
        return load_policy_file(str(local_dir / "policy"))

    return fetch_and_load


def _requests_get(url: str, headers: dict, timeout: float):
    import requests

    resp = requests.get(url, headers=headers, timeout=timeout)
    body = resp.json() if resp.status_code == 200 else {}
    return resp.status_code, body


# ─── Manager ────────────────────────────────────────────────────

class EdgePolicyManager:
    """Keeps each robot's assigned policy loaded and serves actions locally.

    Thread-safety: refresh runs on a background thread; action() is called
    from the simulation loop. Assignments and loaded policies are guarded by
    self._lock; each loaded policy has its own lock for predict().
    """

    def __init__(self, api_url: str, api_key: str, fetch_and_load: Callable,
                 refresh_s: float = 10.0, http_get: Callable = _requests_get):
        self.api_url = api_url.rstrip("/")
        self.api_key = api_key
        self.refresh_s = refresh_s
        self._fetch_and_load = fetch_and_load
        self._http_get = http_get
        self._lock = threading.Lock()
        self._robots: list[str] = []
        self._assigned: dict[str, str] = {}  # robot_id -> artifact_url
        self._loaded: dict[str, tuple] = {}  # artifact_url -> (policy, lock)
        self._stop = threading.Event()
        self._thread: Optional[threading.Thread] = None

    @classmethod
    def from_env(cls) -> "EdgePolicyManager":
        fetch = make_s3_fetch_and_load(
            endpoint=os.environ.get("S3_ENDPOINT", "minio:9000"),
            access_key=os.environ.get("S3_ACCESS_KEY", "fleetos"),
            secret_key=os.environ.get("S3_SECRET_KEY", "fleetos123"),
            bucket=os.environ.get("MODELS_BUCKET", "fleetos-models"),
            cache_dir=os.environ.get("POLICY_CACHE_DIR", "/tmp/fleetos-edge-policies"),
        )
        return cls(api_url=os.environ.get("PLATFORM_API_URL", "http://api:8080"),
                   api_key=os.environ.get("PLATFORM_API_KEY", "dev-key-002"),
                   fetch_and_load=fetch,
                   refresh_s=float(os.environ.get("POLICY_REFRESH_S", "10")))

    def register_robot(self, robot_id: str) -> None:
        with self._lock:
            if robot_id not in self._robots:
                self._robots.append(robot_id)

    def action(self, robot_id: str, obs: np.ndarray) -> Optional[np.ndarray]:
        with self._lock:
            entry = self._loaded.get(self._assigned.get(robot_id, ""))
        if entry is None:
            return None
        policy, policy_lock = entry
        with policy_lock:  # SB3 predict() isn't safe to call concurrently
            act, _ = policy.predict(np.asarray(obs, dtype=np.float32), deterministic=True)
        # Loaded policies return joint targets (JointTargetPolicy for residual
        # ones); the physics step clips to the actuator limits like training.
        return np.asarray(act, dtype=np.float32)

    def refresh_once(self) -> None:
        with self._lock:
            robots = list(self._robots)
        for rid in robots:
            self._refresh_robot(rid)
        self._drop_unused()

    def _refresh_robot(self, rid: str) -> None:
        url = f"{self.api_url}/api/v1/robots/{rid}/policy"
        try:
            status, body = self._http_get(url, {"X-API-Key": self.api_key}, HTTP_TIMEOUT_S)
        except Exception as exc:
            log.warning("policy lookup for %s failed: %s (keeping current policy)", rid, exc)
            return
        if status == 404:
            with self._lock:
                had = self._assigned.pop(rid, None)
            if had:
                log.info("robot %s has no assigned policy: falling back to cloud inference", rid)
            return
        if status != 200 or not body.get("artifact_url"):
            log.warning("policy lookup for %s returned %s (keeping current policy)", rid, status)
            return

        artifact = body["artifact_url"]
        with self._lock:
            if self._assigned.get(rid) == artifact:
                return
            loaded = artifact in self._loaded
        if not loaded:
            try:
                policy = self._fetch_and_load(artifact)
            except Exception as exc:
                log.error("load policy %s for %s failed: %s (keeping current policy)", artifact, rid, exc)
                return
            with self._lock:
                self._loaded.setdefault(artifact, (policy, threading.Lock()))
        with self._lock:
            self._assigned[rid] = artifact
        log.info("robot %s now runs %s (%s) locally [%s]", rid, body.get("model_id"), artifact, body.get("source"))

    def _drop_unused(self) -> None:
        with self._lock:
            in_use = set(self._assigned.values())
            for artifact in [a for a in self._loaded if a not in in_use]:
                del self._loaded[artifact]

    def start(self) -> None:
        def loop():
            while not self._stop.is_set():
                try:
                    self.refresh_once()
                except Exception as exc:  # never let the refresher die
                    log.error("edge policy refresh failed: %s", exc)
                self._stop.wait(self.refresh_s)

        self._thread = threading.Thread(target=loop, name="edge-policy-refresh", daemon=True)
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        if self._thread:
            self._thread.join(timeout=HTTP_TIMEOUT_S * 2)
