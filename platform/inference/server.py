"""
FleetOS Inference Service — SB3 PPO Policy for MuJoCo Humanoid-v4

Loads a pre-trained Stable Baselines3 PPO policy from S3/MinIO.
Training is done externally via platform/training/train_locomotion.py.
Models flow through the platform model registry (staged → canary → deployed).

The /predict API accepts an optional MuJoCo observation vector.
If none is provided, the model runs from a default standing pose.
Instruction keywords bias the raw policy output for command-specific motion.
"""

from __future__ import annotations

import hashlib
import json
import logging
import math
import os
import re
import threading
import time
import urllib.request
import urllib.error
from collections import OrderedDict
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path

import numpy as np

try:
    import redis as redis_lib
    _REDIS_AVAILABLE = True
except ImportError:
    _REDIS_AVAILABLE = False

from vector_search import VectorIndex, robot_to_text, text_to_embedding, HAS_FAISS

# ─── Configuration ───────────────────────────────────────────────

HUMANOID_JOINTS = [
    "head_pan", "head_tilt",
    "left_shoulder_pitch", "left_shoulder_roll", "left_elbow",
    "right_shoulder_pitch", "right_shoulder_roll", "right_elbow",
    "left_hip_yaw", "left_hip_roll", "left_hip_pitch", "left_knee",
    "left_ankle_pitch", "left_ankle_roll",
    "right_hip_yaw", "right_hip_roll", "right_hip_pitch", "right_knee",
    "right_ankle_pitch", "right_ankle_roll",
    "right_gripper_left", "right_gripper_right",
    "left_gripper_left", "left_gripper_right",
    "torso_roll",
]

# MuJoCo humanoid has 25 actuators (17 body + 4 ankle + 4 gripper)
MUJOCO_ACTION_DIM = 25
FLEET_ACTION_DIM = len(HUMANOID_JOINTS)  # 25 — one fleet joint per actuator

# Maps MuJoCo actuator index → HUMANOID_JOINTS index
MUJOCO_TO_FLEET = {
    0: 0,    # abdomen_y → head_pan
    1: 1,    # abdomen_z → head_tilt
    2: 24,   # abdomen_x → torso_roll
    3: 15,   # right_hip_x → right_hip_roll
    4: 14,   # right_hip_z → right_hip_yaw
    5: 16,   # right_hip_y → right_hip_pitch
    6: 17,   # right_knee → right_knee
    7: 9,    # left_hip_x → left_hip_roll
    8: 8,    # left_hip_z → left_hip_yaw
    9: 10,   # left_hip_y → left_hip_pitch
    10: 11,  # left_knee → left_knee
    11: 5,   # right_shoulder1 → right_shoulder_pitch
    12: 6,   # right_shoulder2 → right_shoulder_roll
    13: 7,   # right_elbow → right_elbow
    14: 2,   # left_shoulder1 → left_shoulder_pitch
    15: 3,   # left_shoulder2 → left_shoulder_roll
    16: 4,   # left_elbow → left_elbow
    17: 18,  # right_ankle_y → right_ankle_pitch
    18: 19,  # right_ankle_x → right_ankle_roll  (note: swapped with left in FLEET schema)
    19: 12,  # left_ankle_y → left_ankle_pitch
    20: 13,  # left_ankle_x → left_ankle_roll
    21: 20,  # right_gripper_l → right_gripper_left
    22: 21,  # right_gripper_r → right_gripper_right
    23: 22,  # left_gripper_l → left_gripper_left
    24: 23,  # left_gripper_r → left_gripper_right
}

MODEL_DIR = Path(os.environ.get("MODEL_DIR", "/tmp/fleetos-models"))
MODEL_PATH = MODEL_DIR / "humanoid-v4-ppo"
S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "minio:9000")
S3_BUCKET = os.environ.get("S3_BUCKET", "fleetos-models")
S3_ACCESS_KEY = os.environ.get("S3_ACCESS_KEY", "fleetos")
S3_SECRET_KEY = os.environ.get("S3_SECRET_KEY", "fleetos123")
S3_MODEL_PREFIX = os.environ.get("S3_MODEL_PREFIX", "models/humanoid-v4-ppo")

# Unified observation dimension (from observation.py)
OBS_DIM = 86
# Observation layout version this service builds observations for. Must match
# platform/training/observation.py OBS_VERSION (checked by test_parity.py).
OBS_VERSION = 3
META_FILENAME = "policy_meta.json"
# How policy outputs map to joint targets. Mirrors policy_action.py in
# platform/training and playground/simulator (checked by test_parity.py).
ACTION_ABSOLUTE = 1        # outputs are the joint targets (policies without an action_version)
ACTION_RESIDUAL = 2        # outputs in [-1, 1] offset the standing pose by up to RESIDUAL_SCALE
RESIDUAL_SCALE = 0.5       # rad
STAND_JOINT_ANGLE = 0.0    # rad: every joint in lab_room.xml's "stand" keyframe
# Also accept legacy 376-dim for backward compatibility
LEGACY_OBS_DIM = 376

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger("inference")


# ─── Active Model State ──────────────────────────────────────────

from dataclasses import dataclass, field

@dataclass
class ActiveModel:
    model_id: str = "ppo-humanoid-v4"
    version: str = "v1.0.0"
    artifact_url: str = ""
    ready: bool = False
    _lock: threading.Lock = field(default_factory=threading.Lock, compare=False, repr=False)

    def update(self, model_id: str, version: str, artifact_url: str = "") -> None:
        with self._lock:
            prev = self.model_id
            self.model_id = model_id
            self.version = version
            self.artifact_url = artifact_url
        log.info("[Model] Active model updated: %s → %s (version=%s)", prev, model_id, version)

    def set_ready(self) -> None:
        with self._lock:
            self.ready = True

    def is_ready(self) -> bool:
        with self._lock:
            return self.ready

    def snapshot(self) -> tuple[str, str]:
        with self._lock:
            return self.model_id, self.version


_active_model = ActiveModel()

# Global policy reference — set in main() after loading/training
_policy = None
_policy_lock = threading.Lock()

# ─── Vector Search (Semantic Command Resolution) ────────────────

_vector_index = VectorIndex()
_vector_index_lock = threading.Lock()

# Maps instruction verbs to command types for semantic resolution
VERB_COMMAND_MAP = [
    (["go to", "move to", "bring", "navigate", "head to", "approach"], "move"),
    (["wave", "hello", "greet"], "wave"),
    (["dance"], "dance"),
    (["stop", "halt"], "stop"),
    (["sit", "crouch"], "sit"),
    (["jump", "hop"], "jump"),
    (["look", "scan", "find", "search"], "look_around"),
    (["bow", "respect"], "bow"),
    (["stretch", "warm up"], "stretch"),
    (["walk", "move", "go", "forward", "ahead"], "move_relative"),
]

RESOLVE_CONFIDENCE_THRESHOLD = float(os.environ.get("RESOLVE_CONFIDENCE_THRESHOLD", "0.3"))


def _match_verb(instruction: str) -> str:
    """Match instruction to a command type via verb keywords."""
    inst = instruction.lower()
    for verbs, cmd_type in VERB_COMMAND_MAP:
        for verb in verbs:
            if verb in inst:
                return cmd_type
    return ""


def resolve_command(instruction: str, robot_id: str = "") -> dict:
    """Resolve a natural language instruction to a structured command using vector search.

    Searches the FAISS index for robots matching the instruction context,
    then maps instruction verbs to a concrete command type with params
    derived from the search results (e.g., target position from matched robots).
    """
    with _vector_index_lock:
        results = _vector_index.search(instruction, top_k=5)

    cmd_type = _match_verb(instruction)
    top_score = results[0]["score"] if results else 0.0

    # If we have search results with good confidence, use them for context
    if results and top_score >= RESOLVE_CONFIDENCE_THRESHOLD:
        # Extract average position from top matching robots for move commands
        if cmd_type in ("move", "move_relative", ""):
            positions = []
            for r in results[:3]:
                desc = r.get("description", "")
                # Parse position from description: "position x=1.2 y=3.4"
                x_match = re.search(r'x=([-\d.]+)', desc)
                y_match = re.search(r'y=([-\d.]+)', desc)
                if x_match and y_match:
                    positions.append((float(x_match.group(1)), float(y_match.group(1))))
            if positions:
                avg_x = sum(p[0] for p in positions) / len(positions)
                avg_y = sum(p[1] for p in positions) / len(positions)
                if not cmd_type:
                    cmd_type = "move"
                return {
                    "type": cmd_type,
                    "params": {
                        "x": round(avg_x, 2),
                        "y": round(avg_y, 2),
                        "instruction": instruction,
                    },
                    "confidence": round(top_score, 4),
                    "context": results[:3],
                }

    # No spatial context needed — just resolve the verb
    if cmd_type:
        return {
            "type": cmd_type,
            "params": {"instruction": instruction},
            "confidence": round(top_score, 4) if results else 0.0,
            "context": results[:3] if results else [],
        }

    # No match at all — return empty so caller falls back to apply_actions
    return {
        "type": "",
        "params": {"instruction": instruction},
        "confidence": 0.0,
        "context": results[:3] if results else [],
    }


def _poll_robot_states(redis_addr: str) -> None:
    """Background thread: poll Redis for robot hot state and update FAISS index."""
    if not _REDIS_AVAILABLE:
        log.warning("[VectorSearch] redis package not available — index updates disabled")
        return

    host, _, port_str = redis_addr.rpartition(":")
    port = int(port_str) if port_str.isdigit() else 6379

    while True:
        try:
            client = redis_lib.Redis(host=host or "localhost", port=port, decode_responses=True)
            log.info("[VectorSearch] Polling robot states from Redis %s every 5s", redis_addr)

            while True:
                try:
                    keys = []
                    cursor = 0
                    while True:
                        cursor, batch = client.scan(cursor, match="robot:state:*", count=100)
                        keys.extend(batch)
                        if cursor == 0:
                            break

                    if keys:
                        pipe = client.pipeline()
                        for key in keys:
                            pipe.get(key)
                        values = pipe.execute()

                        robots = []
                        for key, val in zip(keys, values):
                            if val:
                                try:
                                    state = json.loads(val)
                                    robot_id = key.split(":", 2)[-1] if ":" in key else key
                                    state["robot_id"] = robot_id
                                    robots.append(state)
                                except (json.JSONDecodeError, KeyError):
                                    pass

                        if robots:
                            new_index = VectorIndex()
                            new_index.index_robots(robots)
                            with _vector_index_lock:
                                global _vector_index
                                _vector_index = new_index
                            log.debug("[VectorSearch] Indexed %d robots", len(robots))
                except Exception as exc:
                    log.warning("[VectorSearch] Poll error: %s", exc)

                time.sleep(5)
        except Exception as exc:
            log.error("[VectorSearch] Redis connection failed: %s — retrying in 5s", exc)
            time.sleep(5)


def _poll_platform_model(platform_api_url: str) -> str:
    """On startup, fetch the currently deployed model from the platform API.

    Returns the artifact_url of the deployed model, or empty string.
    """
    url = f"{platform_api_url.rstrip('/')}/api/v1/models?status=deployed"
    req = urllib.request.Request(url)
    # The models API is authenticated; without a key this 401s and inference
    # silently restarts on the zero-action stub instead of the deployed model.
    api_key = os.environ.get("PLATFORM_API_KEY", "")
    if api_key:
        req.add_header("X-API-Key", api_key)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            data = json.loads(resp.read())
        models = data.get("models") or []
        if models:
            m = models[0]
            _active_model.update(m["id"], m["version"], m.get("artifact_url", ""))
            log.info("[Model] Loaded deployed model from platform: %s", m["id"])
            return m.get("artifact_url", "")
        else:
            log.info("[Model] No deployed model found on platform, using default")
            return ""
    except Exception as exc:
        log.warning("[Model] Could not reach platform API (%s): %s — using default", url, exc)
        return ""


def _hot_swap_model(artifact_url: str) -> None:
    """Download and swap in a new model from S3."""
    global _policy
    try:
        # Remove cached zip so we re-download
        zip_path = str(MODEL_PATH) + ".zip"
        if os.path.exists(zip_path):
            os.remove(zip_path)

        if _download_model_from_s3(artifact_url):
            new_model = load_policy_file(str(MODEL_PATH))
            with _policy_lock:
                _policy = new_model
            log.info("[Model] Hot-swapped to new model from %s", artifact_url)
        else:
            log.warning("[Model] Hot-swap failed: could not download from %s", artifact_url)
    except Exception as e:
        log.error("[Model] Hot-swap error: %s", e)


def _subscribe_model_updates(redis_addr: str) -> None:
    """Background thread: subscribe to Redis model:deployed events."""
    if not _REDIS_AVAILABLE:
        log.warning("[Model] redis package not available — model hot-swap disabled")
        return

    host, _, port_str = redis_addr.rpartition(":")
    port = int(port_str) if port_str.isdigit() else 6379

    while True:
        try:
            client = redis_lib.Redis(host=host or "localhost", port=port, decode_responses=True)
            pubsub = client.pubsub()
            pubsub.subscribe("model:deployed")
            log.info("[Model] Subscribed to Redis model:deployed on %s", redis_addr)

            for message in pubsub.listen():
                if message["type"] != "message":
                    continue
                try:
                    payload = json.loads(message["data"])
                    new_artifact_url = payload.get("artifact_url", "")
                    _active_model.update(
                        payload["model_id"],
                        payload["version"],
                        new_artifact_url,
                    )
                    if new_artifact_url:
                        _hot_swap_model(new_artifact_url)
                except Exception as exc:
                    log.error("[Model] Failed to process model:deployed event: %s", exc)
        except Exception as exc:
            log.error("[Model] Redis subscriber disconnected: %s — retrying in 5s", exc)
            time.sleep(5)


# ─── Model Loading ──────────────────────────────────────────────

def _parse_s3_key(artifact_url: str) -> str:
    """Extract S3 object key from artifact_url.

    Handles: 's3://bucket/key', or bare key like 'training/job123/policy.zip'.
    """
    if artifact_url.startswith("s3://"):
        parts = artifact_url[5:]  # strip s3://
        _, _, key = parts.partition("/")  # strip bucket name
        return key
    return artifact_url


def _download_model_from_s3(artifact_url: str = "", zip_path: str | None = None) -> bool:
    """Download the model from S3/MinIO.

    Args:
        artifact_url: S3 key or s3:// URL from the model registry.
                      Falls back to S3_MODEL_PREFIX if empty.
        zip_path: Destination file. Defaults to the active model's MODEL_PATH.zip.
    """
    zip_path = zip_path or str(MODEL_PATH) + ".zip"
    try:
        from minio import Minio
        client = Minio(S3_ENDPOINT, access_key=S3_ACCESS_KEY, secret_key=S3_SECRET_KEY, secure=False)

        if artifact_url:
            s3_key = _parse_s3_key(artifact_url)
        else:
            s3_key = S3_MODEL_PREFIX + "/policy.zip"

        Path(zip_path).parent.mkdir(parents=True, exist_ok=True)
        client.fget_object(S3_BUCKET, s3_key, zip_path)
        log.info("[Model] Downloaded from s3://%s/%s", S3_BUCKET, s3_key)
        # Normalization stats are optional (offline models have none).
        norm_local = normalizer_path(zip_path)
        if os.path.exists(norm_local):
            os.remove(norm_local)  # never pair a new policy with stale stats
        try:
            client.fget_object(S3_BUCKET, normalizer_key(s3_key), norm_local)
            log.info("[Model] Downloaded normalization stats %s", normalizer_key(s3_key))
        except Exception as norm_err:
            log.info("[Model] No normalization stats for %s (%s) — serving raw observations",
                     s3_key, norm_err)
        # Compatibility metadata: missing/stale means the policy is refused.
        meta_local = str(Path(zip_path).parent / META_FILENAME)
        if os.path.exists(meta_local):
            os.remove(meta_local)
        try:
            client.fget_object(S3_BUCKET, str(Path(s3_key).parent / META_FILENAME), meta_local)
        except Exception as meta_err:
            log.warning("[Model] No %s for %s (%s)", META_FILENAME, s3_key, meta_err)
        return True
    except ImportError:
        log.warning("[Model] minio package not installed — cannot download from S3")
        return False
    except Exception as e:
        log.warning("[Model] Could not download model from S3: %s", e)
        return False


# ─── Observation normalization ──────────────────────────────────
# PPO policies are trained behind VecNormalize; serving must feed them
# observations scaled exactly as in training. The stats ship next to the
# policy as vec_normalize.pkl (offline CQL models have none: raw obs).

NORMALIZER_FILE = "vec_normalize.pkl"


def normalize_obs(obs, mean, var, clip_obs: float, epsilon: float) -> np.ndarray:
    """VecNormalize's observation transform: clip((obs - mean) / sqrt(var + eps))."""
    out = (np.asarray(obs, dtype=np.float64) - mean) / np.sqrt(var + epsilon)
    return np.clip(out, -clip_obs, clip_obs).astype(np.float32)


def normalizer_path(policy_path: str) -> str:
    """Local vec_normalize.pkl that belongs to a policy file (same directory)."""
    return str(Path(policy_path).parent / NORMALIZER_FILE)


def normalizer_key(artifact_url: str) -> str:
    """S3 key of the vec_normalize.pkl uploaded alongside an artifact."""
    key = _parse_s3_key(artifact_url)
    return str(Path(key).parent / NORMALIZER_FILE)


def load_obs_normalizer(path: str):
    """Return obs -> normalized obs from a saved VecNormalize, or None if absent."""
    if not os.path.exists(path):
        return None
    import pickle
    with open(path, "rb") as f:
        vec = pickle.load(f)  # trusted: written by our own training jobs
    mean, var = np.asarray(vec.obs_rms.mean), np.asarray(vec.obs_rms.var)
    clip_obs, epsilon = float(vec.clip_obs), float(vec.epsilon)
    return lambda obs: normalize_obs(obs, mean, var, clip_obs, epsilon)


class NormalizedPolicy:
    """Wraps a policy so predict() sees training-scaled observations."""

    def __init__(self, policy, normalize):
        self._policy = policy
        self._normalize = normalize
        self.observation_space = getattr(policy, "observation_space", None)

    def predict(self, obs, deterministic=False):
        if isinstance(obs, dict):  # goal-conditioned policies aren't trained behind VecNormalize
            return self._policy.predict(obs, deterministic=deterministic)
        return self._policy.predict(self._normalize(obs), deterministic=deterministic)


class _StubPolicy:
    """Produces zero-vector actions when no trained model is available.

    The instruction bias layer still generates command-responsive motion on
    top of the zero baseline, so robots respond to commands even without a
    trained model.
    """

    def predict(self, obs, deterministic=False):
        return np.zeros(MUJOCO_ACTION_DIM, dtype=np.float32), None


class IncompatiblePolicy(RuntimeError):
    """The policy was trained on a different observation layout; never serve it."""


def read_policy_meta(meta_path: str) -> dict | None:
    if not os.path.exists(meta_path):
        return None
    with open(meta_path) as f:
        return json.load(f)


def policy_incompatibility(meta: dict | None) -> str | None:
    """Why a policy with this policy_meta.json can't be served, or None."""
    if meta is None:
        return (f"no {META_FILENAME}: policy predates observation version {OBS_VERSION} "
                "(world-frame observations); retrain it")
    version = meta.get("obs_version")
    if version != OBS_VERSION:
        return f"policy uses observation version {version}, this build uses {OBS_VERSION}; retrain it"
    action_version = meta.get("action_version", ACTION_ABSOLUTE)
    if action_version not in (ACTION_ABSOLUTE, ACTION_RESIDUAL):
        return f"policy uses action version {action_version}, this build supports 1 and 2"
    return None


class JointTargetPolicy:
    """Serves a residual policy's outputs as joint targets around the standing pose."""

    def __init__(self, policy):
        self._policy = policy
        self.observation_space = getattr(policy, "observation_space", None)

    def predict(self, obs, deterministic=False):
        action, state = self._policy.predict(obs, deterministic=deterministic)
        offsets = RESIDUAL_SCALE * np.clip(np.asarray(action, dtype=np.float32), -1.0, 1.0)
        return STAND_JOINT_ANGLE + offsets, state


def load_policy_file(path: str):
    """Load an SB3 policy saved at `path` (with or without .zip).

    Locomotion policies are PPO; reach/grasp policies are SAC (+HER). The zip
    doesn't say which, so try PPO first and fall back to SAC. Policies trained
    on another observation version are refused (IncompatiblePolicy); residual
    policies are wrapped so every served action is joint targets.
    """
    meta = read_policy_meta(str(Path(path).parent / META_FILENAME))
    reason = policy_incompatibility(meta)
    if reason:
        raise IncompatiblePolicy(f"{path}: {reason}")
    policy = _load_sb3(path)
    normalize = load_obs_normalizer(normalizer_path(path))
    if normalize is not None:
        log.info("[Model] Applying observation normalization from %s", normalizer_path(path))
        policy = NormalizedPolicy(policy, normalize)
    if meta.get("action_version", ACTION_ABSOLUTE) == ACTION_RESIDUAL:
        policy = JointTargetPolicy(policy)
    return policy


def _load_sb3(path: str):
    from stable_baselines3 import PPO, SAC

    try:
        return PPO.load(path)
    except Exception as ppo_err:
        try:
            # SAC+HER policies refuse to load without an env (HerReplayBuffer
            # needs one). Serving never touches the replay buffer, so swap in
            # the plain dict buffer.
            from stable_baselines3.common.buffers import DictReplayBuffer
            model = SAC.load(path, custom_objects={
                "replay_buffer_class": DictReplayBuffer,
                "replay_buffer_kwargs": {},
            })
            log.info("[Model] Loaded %s as SAC", path)
            return model
        except Exception as sac_err:
            raise RuntimeError(f"load policy {path}: not PPO ({ppo_err}) or SAC ({sac_err})") from sac_err


class PolicyCache:
    """Thread-safe LRU of loaded policies keyed by artifact_url.

    Lets canary robots run a different model than the fleet default: the API
    passes the robot's assigned artifact_url and we serve that policy. Each
    entry carries its own lock because SB3 predict() isn't safe to call
    concurrently on one model.
    """

    def __init__(self, capacity: int, fetch):
        self._capacity = max(1, capacity)
        self._fetch = fetch  # artifact_url -> policy (raises on failure)
        self._entries: "OrderedDict[str, tuple[object, threading.Lock]]" = OrderedDict()
        self._lock = threading.Lock()

    def get(self, artifact_url: str) -> tuple[object, threading.Lock]:
        with self._lock:
            entry = self._entries.get(artifact_url)
            if entry is not None:
                self._entries.move_to_end(artifact_url)
                return entry

        # Load outside the cache lock so a slow S3 download doesn't block hits.
        policy = self._fetch(artifact_url)
        entry = (policy, threading.Lock())
        with self._lock:
            self._entries[artifact_url] = entry
            self._entries.move_to_end(artifact_url)
            while len(self._entries) > self._capacity:
                evicted, _ = self._entries.popitem(last=False)
                log.info("[Model] Evicted cached policy %s", evicted)
        return entry

    def keys(self) -> list[str]:
        with self._lock:
            return list(self._entries)


def _artifact_cache_path(artifact_url: str) -> str:
    """Per-artifact download path (never MODEL_PATH, which hot-swap deletes)."""
    digest = hashlib.sha1(artifact_url.encode()).hexdigest()[:16]
    return str(MODEL_DIR / "cache" / digest / "policy")


def _fetch_policy(artifact_url: str):
    path = _artifact_cache_path(artifact_url)
    if not os.path.exists(path + ".zip") and not _download_model_from_s3(artifact_url, path + ".zip"):
        raise RuntimeError(f"download {artifact_url} failed")
    return load_policy_file(path)


POLICY_CACHE_SIZE = int(os.environ.get("INFERENCE_POLICY_CACHE_SIZE", "4"))
_policy_cache = PolicyCache(POLICY_CACHE_SIZE, _fetch_policy)


def load_model(artifact_url: str = ""):
    """Load a pre-trained policy (PPO or SAC) from cache or S3.

    Training is handled by platform/training/ — this service only serves inference.
    """
    zip_path = str(MODEL_PATH) + ".zip"

    try:
        # Try local cache first
        if os.path.exists(zip_path):
            log.info("[Model] Loading cached model from %s", zip_path)
            model = load_policy_file(str(MODEL_PATH))
            log.info("[Model] Model loaded successfully")
            return model

        # Try downloading from S3
        if _download_model_from_s3(artifact_url) and os.path.exists(zip_path):
            log.info("[Model] Loading downloaded model from %s", zip_path)
            model = load_policy_file(str(MODEL_PATH))
            log.info("[Model] Model loaded successfully")
            return model
    except IncompatiblePolicy as exc:
        log.error("[Model] Refusing incompatible policy: %s", exc)

    # No model available — use stub that produces zero actions
    log.warning("[Model] No trained model found. Using zero-action stub.")
    log.warning("[Model] Train via: python platform/training/train_locomotion.py --job-id <id> --s3-endpoint minio:9000")
    return _StubPolicy()


def get_default_observation() -> np.ndarray:
    """Generate a default standing observation (unified 86-dim format).

    Components: joint_pos(25) + joint_vel(25) + root_quat(4) + root_linvel(3) +
    root_angvel(3) + hand_right(3) + hand_left(3) + gripper(4) + obj_pos(6) +
    obj_vel(6) + task_encoding(4) = 86
    """
    obs = np.zeros(OBS_DIM, dtype=np.float32)
    # Standing pose: root quaternion w=1 (upright)
    # qpos-derived features occupy roughly the first ~45 dims.
    # Set a small upright z-height signal and zero everything else.
    obs[0] = 1.25   # approximate z-height of standing humanoid
    return obs


# ─── Inference Pipeline ──────────────────────────────────────────

def mujoco_to_fleet_actions(mujoco_actions: np.ndarray) -> list[dict]:
    """Convert 25-dim MuJoCo actions to the 25-joint fleet schema (one joint per actuator)."""
    fleet_torques = np.zeros(FLEET_ACTION_DIM, dtype=np.float32)
    for mj_idx, fleet_idx in MUJOCO_TO_FLEET.items():
        fleet_torques[fleet_idx] = mujoco_actions[mj_idx]  # joint range; robots clip per actuator

    predicted_actions = []
    for j, joint_name in enumerate(HUMANOID_JOINTS):
        predicted_actions.append({
            "joint": joint_name,
            "position": round(float(fleet_torques[j]) * 0.5, 4),
            "velocity": 0.0,
            "torque": round(float(fleet_torques[j]), 4),
        })
    return predicted_actions


def apply_instruction_bias(actions: np.ndarray, instruction: str) -> np.ndarray:
    """Bias the raw policy output based on instruction keywords.

    The base PPO policy learns locomotion/balance. We layer
    instruction-specific overrides on top for command responsiveness.
    """
    inst = instruction.lower()
    out = actions.copy()

    # MuJoCo Humanoid-v4 actuator indices
    ABDOMEN_Z, ABDOMEN_Y = 0, 1
    R_HIP_X, R_HIP_Z, R_HIP_Y, R_KNEE = 3, 4, 5, 6
    L_HIP_X, L_HIP_Z, L_HIP_Y, L_KNEE = 7, 8, 9, 10
    R_SHOULDER1, R_SHOULDER2, R_ELBOW = 11, 12, 13
    L_SHOULDER1, L_SHOULDER2, L_ELBOW = 14, 15, 16

    if "wave" in inst:
        out[R_SHOULDER1] = -0.8
        out[R_SHOULDER2] = 0.4
        out[R_ELBOW] = 0.6 * math.sin(time.time() * 6)
    elif "pick" in inst or "grab" in inst or "grasp" in inst:
        out[L_SHOULDER1] = 0.7
        out[R_SHOULDER1] = 0.7
        out[L_ELBOW] = -0.5
        out[R_ELBOW] = -0.5
    elif "walk" in inst or "move" in inst or "go" in inst:
        t = time.time() * 3.6 * math.pi
        out[R_HIP_Y] += 0.4 * math.sin(t)
        out[L_HIP_Y] -= 0.4 * math.sin(t)
        out[R_KNEE] += 0.3 * max(0, math.sin(t))
        out[L_KNEE] += 0.3 * max(0, -math.sin(t))
    elif "sit" in inst:
        out[R_HIP_Y] = -0.8
        out[L_HIP_Y] = -0.8
        out[R_KNEE] = 0.9
        out[L_KNEE] = 0.9
    elif "dance" in inst:
        t = time.time() * 5 * math.pi
        out[ABDOMEN_Z] = 0.5 * math.sin(t)
        out[R_SHOULDER1] = 0.6 * math.sin(t)
        out[L_SHOULDER1] = 0.6 * math.sin(t + 1)
        out[R_HIP_Y] += 0.3 * math.sin(t)
        out[L_HIP_Y] += 0.3 * math.sin(t)
    elif "bow" in inst:
        out[ABDOMEN_Y] = 0.6
        out[R_HIP_Y] = -0.5
        out[L_HIP_Y] = -0.5
    elif "jump" in inst:
        out[R_HIP_Y] = 0.9
        out[L_HIP_Y] = 0.9
        out[R_KNEE] = -0.9
        out[L_KNEE] = -0.9
    elif "stop" in inst or "stand" in inst or "still" in inst:
        out *= 0.1  # dampen all actions for standing

    return np.clip(out, -1.0, 1.0)


# Right-hand position inside the unified 86-dim observation:
# joint_pos(25) + joint_vel(25) + root_quat(4) + root_linvel(3) + root_angvel(3) = 60.
HAND_RIGHT_SLICE = slice(60, 63)


def build_policy_obs(policy, obs: np.ndarray, goal: list | None):
    """Shape the observation for the policy.

    Goal-conditioned policies (SAC+HER reach/grasp) expect a dict of
    observation / achieved_goal / desired_goal; everything else takes the flat
    86-dim vector. achieved_goal is the right-hand position (what the reach env
    uses); desired_goal comes from the request, or the hand itself if absent.
    """
    spaces = getattr(getattr(policy, "observation_space", None), "spaces", None)
    if not spaces or "desired_goal" not in spaces:
        return obs
    achieved = obs[HAND_RIGHT_SLICE].astype(np.float32)
    desired = np.asarray(goal, dtype=np.float32)[:3] if goal else achieved.copy()
    return {"observation": obs, "achieved_goal": achieved, "desired_goal": desired}


def select_policy(artifact_url: str = "", model_id: str = "", model_version: str = ""):
    """Pick the policy for a request.

    No artifact_url (or the active model's own artifact) → the active global
    policy. Otherwise the artifact's policy from the LRU cache — this is how a
    canary robot gets its assigned model. If that policy can't be loaded we
    serve the active policy instead and flag the response as a fallback.

    Returns (policy, lock, model_id, model_version, fallback).
    """
    active_id, active_version = _active_model.snapshot()
    if artifact_url and artifact_url != _active_model.artifact_url:
        try:
            policy, lock = _policy_cache.get(artifact_url)
            return policy, lock, model_id or artifact_url, model_version, False
        except Exception as exc:
            log.warning("[Model] Could not load %s (%s) — falling back to active model %s",
                        artifact_url, exc, active_id)
            return _policy, _policy_lock, active_id, active_version, True
    return _policy, _policy_lock, active_id, active_version, False


def run_inference(observation: list | None, instruction: str, model_id: str, embodiment: str,
                  artifact_url: str = "", model_version: str = "", goal: list | None = None) -> dict:
    """
    Run the trained policy on the given observation.

    Args:
        observation: Optional 86-dim (or legacy 376-dim) observation. Uses default if None.
        instruction: Natural language command to bias the action output.
        model_id: Registry model ID requested by the caller (e.g. a canary assignment).
        embodiment: Robot embodiment type.
        artifact_url: S3 artifact of the requested model; empty = active model.
        model_version: Registry version of the requested model.
    """
    pipeline_start = time.perf_counter()

    policy, policy_lock, model_id, model_version, fallback = select_policy(
        artifact_url, model_id, model_version)

    log.info("=" * 60)
    log.info("INFERENCE REQUEST")
    log.info("  Model: %s | Embodiment: %s", model_id, embodiment)
    log.info("  Instruction: '%s'", instruction)
    log.info("  Observation: %s", f"{len(observation)}-dim" if observation else "default standing pose")
    log.info("-" * 60)

    # Build observation — accept both unified (86-dim) and legacy (376-dim)
    if observation and len(observation) in (OBS_DIM, LEGACY_OBS_DIM):
        obs = np.array(observation, dtype=np.float32)
        # If legacy 376-dim, pad/truncate to OBS_DIM for new policies
        if len(obs) == LEGACY_OBS_DIM and len(obs) != OBS_DIM:
            obs = obs[:OBS_DIM] if LEGACY_OBS_DIM > OBS_DIM else np.pad(obs, (0, OBS_DIM - len(obs)))
    else:
        obs = get_default_observation()

    # Run policy
    served_policy = policy
    try:
        with policy_lock:
            raw_action, _states = policy.predict(build_policy_obs(policy, obs, goal), deterministic=True)
    except Exception as exc:
        if policy is _policy:
            raise
        # A canary policy that can't handle this request must not take the
        # robot down — serve the active model instead.
        log.warning("[Model] Policy %s failed to predict (%s) — falling back to active model",
                    model_id, exc)
        model_id, model_version = _active_model.snapshot()
        fallback = True
        served_policy = _policy
        with _policy_lock:
            raw_action, _states = _policy.predict(build_policy_obs(_policy, obs, goal), deterministic=True)

    raw_action = np.asarray(raw_action, dtype=np.float32)
    log.info("  [Policy] Raw action (17-dim): mean=%.3f, std=%.3f, range=[%.3f, %.3f]",
             raw_action.mean(), raw_action.std(), raw_action.min(), raw_action.max())

    # Instruction bias only makes the zero-action stub responsive; a learned
    # policy's actions are served unmodified (biasing them breaks its gait).
    bias_applied = isinstance(served_policy, _StubPolicy)
    if bias_applied:
        biased_action = apply_instruction_bias(raw_action, instruction)
        log.info("  [Bias] Stub policy: instruction bias applied")
    else:
        biased_action = raw_action  # robots clip each actuator to its ctrlrange, as in training

    # Convert to fleet joint schema
    predicted_actions = mujoco_to_fleet_actions(biased_action)

    total_ms = (time.perf_counter() - pipeline_start) * 1000

    log.info("-" * 60)
    log.info("RESULT: %d joint actions, latency=%.1fms", len(predicted_actions), total_ms)
    log.info("=" * 60)

    return {
        "predicted_actions": predicted_actions,
        "action": biased_action.tolist(),  # flat 25-dim array for simulator
        "confidence": 0.85,
        "model_id": model_id,
        "model_version": model_version,
        "embodiment": embodiment,
        "task_type": instruction,
        "action_dim": MUJOCO_ACTION_DIM,
        "latency_ms": round(total_ms, 1),
        "fallback": fallback,
        "bias_applied": bias_applied,
    }


# ─── HTTP Server ─────────────────────────────────────────────────

class InferenceHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path == "/predict":
            if not _active_model.is_ready():
                self.send_response(503)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"error": "model still loading/training"}).encode())
                return

            content_len = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_len)) if content_len else {}

            result = run_inference(
                observation=body.get("observation"),
                instruction=body.get("instruction", "stand still"),
                model_id=body.get("model_id", ""),
                embodiment=body.get("embodiment", "humanoid-v4"),
                artifact_url=body.get("artifact_url", ""),
                model_version=body.get("model_version", ""),
                goal=body.get("goal"),
            )

            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(result).encode())

        elif self.path == "/resolve":
            content_len = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_len)) if content_len else {}

            instruction = body.get("instruction", "")
            robot_id = body.get("robot_id", "")

            if not instruction:
                self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"error": "instruction required"}).encode())
                return

            result = resolve_command(instruction, robot_id)

            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(result).encode())

        else:
            self.send_error(404)

    def do_GET(self):
        if self.path == "/health":
            model_id, version = _active_model.snapshot()
            ready = _active_model.is_ready()
            status = "ok" if ready else "loading"
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "status": status,
                "model": model_id,
                "ready": ready,
                "vector_index": {
                    "indexed": len(_vector_index.docs),
                    "backend": "faiss" if HAS_FAISS else "numpy",
                },
            }).encode())
        elif self.path == "/model":
            model_id, version = _active_model.snapshot()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "model_id": model_id,
                "version": version,
                "artifact_url": _active_model.artifact_url,
            }).encode())
        else:
            self.send_error(404)

    def log_message(self, format, *args):
        pass


def _load_model_background():
    """Load the model from cache or S3, then mark as ready."""
    global _policy

    # Check platform API for currently deployed model's artifact_url
    platform_api_url = os.environ.get("PLATFORM_API_URL", "")
    artifact_url = ""
    if platform_api_url:
        artifact_url = _poll_platform_model(platform_api_url)

    _policy = load_model(artifact_url=artifact_url)
    _active_model.set_ready()

    model_id, version = _active_model.snapshot()
    log.info("Model ready: %s (version=%s)", model_id, version)
    log.info("Policy: %d actuators → %d fleet joints",
             MUJOCO_ACTION_DIM, FLEET_ACTION_DIM)


def main():
    port = int(os.environ.get("INFERENCE_PORT", "8081"))

    # Start HTTP server immediately so health checks pass during training
    server = HTTPServer(("0.0.0.0", port), InferenceHandler)
    log.info("FleetOS Inference Service starting on :%d (model loading in background)", port)

    # Load/train model in background thread
    threading.Thread(target=_load_model_background, daemon=True).start()

    # Subscribe to Redis model:deployed events in a background daemon thread
    redis_addr = os.environ.get("REDIS_ADDR", "")
    if redis_addr:
        threading.Thread(target=_subscribe_model_updates, args=(redis_addr,), daemon=True).start()
        threading.Thread(target=_poll_robot_states, args=(redis_addr,), daemon=True).start()
    else:
        log.info("[Model] REDIS_ADDR not set — model hot-swap and vector search disabled")

    server.serve_forever()


if __name__ == "__main__":
    main()
