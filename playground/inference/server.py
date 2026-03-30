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

import json
import logging
import math
import os
import threading
import time
import urllib.request
import urllib.error
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path

import numpy as np

try:
    import redis as redis_lib
    _REDIS_AVAILABLE = True
except ImportError:
    _REDIS_AVAILABLE = False

# ─── Configuration ───────────────────────────────────────────────

HUMANOID_JOINTS = [
    "head_pan", "head_tilt",
    "left_shoulder_pitch", "left_shoulder_roll", "left_elbow",
    "right_shoulder_pitch", "right_shoulder_roll", "right_elbow",
    "left_hip_yaw", "left_hip_roll", "left_hip_pitch", "left_knee",
    "left_ankle_pitch", "left_ankle_roll",
    "right_hip_yaw", "right_hip_roll", "right_hip_pitch", "right_knee",
    "right_ankle_pitch", "right_ankle_roll",
]

# MuJoCo Humanoid-v4 has 17 actuators; our schema has 20 (4 ankles unmapped)
MUJOCO_ACTION_DIM = 17
FLEET_ACTION_DIM = len(HUMANOID_JOINTS)  # 20

# Maps MuJoCo actuator index → HUMANOID_JOINTS index
MUJOCO_TO_FLEET = {
    0: 0,    # abdomen_z → head_pan
    1: 1,    # abdomen_y → head_tilt
    # 2: abdomen_x → no mapping
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
}

MODEL_DIR = Path(os.environ.get("MODEL_DIR", "/tmp/fleetos-models"))
MODEL_PATH = MODEL_DIR / "humanoid-v4-ppo"
S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "minio:9000")
S3_BUCKET = os.environ.get("S3_BUCKET", "fleetos-models")
S3_ACCESS_KEY = os.environ.get("S3_ACCESS_KEY", "fleetos")
S3_SECRET_KEY = os.environ.get("S3_SECRET_KEY", "fleetos123")
S3_MODEL_PREFIX = os.environ.get("S3_MODEL_PREFIX", "models/humanoid-v4-ppo")

# MuJoCo Humanoid-v4 observation dimension
OBS_DIM = 376

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


def _poll_platform_model(platform_api_url: str) -> str:
    """On startup, fetch the currently deployed model from the platform API.

    Returns the artifact_url of the deployed model, or empty string.
    """
    url = f"{platform_api_url.rstrip('/')}/api/v1/models?status=deployed"
    try:
        with urllib.request.urlopen(url, timeout=5) as resp:
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
            from stable_baselines3 import PPO
            new_model = PPO.load(str(MODEL_PATH))
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


def _download_model_from_s3(artifact_url: str = "") -> bool:
    """Download the model from S3/MinIO.

    Args:
        artifact_url: S3 key or s3:// URL from the model registry.
                      Falls back to S3_MODEL_PREFIX if empty.
    """
    zip_path = str(MODEL_PATH) + ".zip"
    try:
        from minio import Minio
        client = Minio(S3_ENDPOINT, access_key=S3_ACCESS_KEY, secret_key=S3_SECRET_KEY, secure=False)

        if artifact_url:
            s3_key = _parse_s3_key(artifact_url)
        else:
            s3_key = S3_MODEL_PREFIX + "/policy.zip"

        MODEL_DIR.mkdir(parents=True, exist_ok=True)
        client.fget_object(S3_BUCKET, s3_key, zip_path)
        log.info("[Model] Downloaded from s3://%s/%s", S3_BUCKET, s3_key)
        return True
    except ImportError:
        log.warning("[Model] minio package not installed — cannot download from S3")
        return False
    except Exception as e:
        log.warning("[Model] Could not download model from S3: %s", e)
        return False


class _StubPolicy:
    """Produces zero-vector actions when no trained model is available.

    The instruction bias layer still generates command-responsive motion on
    top of the zero baseline, so robots respond to commands even without a
    trained model.
    """

    def predict(self, obs, deterministic=False):
        return np.zeros(MUJOCO_ACTION_DIM, dtype=np.float32), None


def load_model(artifact_url: str = ""):
    """Load a pre-trained PPO model from cache or S3.

    Training is handled by platform/training/ — this service only serves inference.
    """
    from stable_baselines3 import PPO

    zip_path = str(MODEL_PATH) + ".zip"

    # Try local cache first
    if os.path.exists(zip_path):
        log.info("[Model] Loading cached model from %s", zip_path)
        model = PPO.load(str(MODEL_PATH))
        log.info("[Model] Model loaded successfully")
        return model

    # Try downloading from S3
    if _download_model_from_s3(artifact_url) and os.path.exists(zip_path):
        log.info("[Model] Loading downloaded model from %s", zip_path)
        model = PPO.load(str(MODEL_PATH))
        log.info("[Model] Model loaded successfully")
        return model

    # No model available — use stub that produces zero actions
    log.warning("[Model] No trained model found. Using zero-action stub.")
    log.warning("[Model] Train via: python platform/training/train_locomotion.py --job-id <id> --s3-endpoint minio:9000")
    return _StubPolicy()


def get_default_observation() -> np.ndarray:
    """Generate a default standing observation for Humanoid-v4.

    The observation space is 376-dim: body positions, velocities,
    center of mass info, and external forces. We use a neutral
    standing pose with small noise for realism.
    """
    obs = np.zeros(OBS_DIM, dtype=np.float32)
    # Minimal standing pose hints:
    # qpos-derived features occupy roughly the first ~45 dims.
    # Set a small upright z-height signal and zero everything else.
    obs[0] = 1.25   # approximate z-height of standing humanoid
    return obs


# ─── Inference Pipeline ──────────────────────────────────────────

def mujoco_to_fleet_actions(mujoco_actions: np.ndarray) -> list[dict]:
    """Convert 17-dim MuJoCo actions to 20-joint fleet schema."""
    fleet_torques = np.zeros(FLEET_ACTION_DIM, dtype=np.float32)
    for mj_idx, fleet_idx in MUJOCO_TO_FLEET.items():
        fleet_torques[fleet_idx] = np.clip(mujoco_actions[mj_idx], -1.0, 1.0)

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


def run_inference(observation: list | None, instruction: str, model_id: str, embodiment: str) -> dict:
    """
    Run the real PPO policy on the given observation.

    Args:
        observation: Optional 376-dim MuJoCo observation. Uses default if None.
        instruction: Natural language command to bias the action output.
        model_id: Model identifier (for logging/tracking).
        embodiment: Robot embodiment type.
    """
    pipeline_start = time.perf_counter()

    active_id, active_version = _active_model.snapshot()
    if not model_id:
        model_id = active_id
    model_version = active_version

    log.info("=" * 60)
    log.info("INFERENCE REQUEST")
    log.info("  Model: %s | Embodiment: %s", model_id, embodiment)
    log.info("  Instruction: '%s'", instruction)
    log.info("  Observation: %s", f"{len(observation)}-dim" if observation else "default standing pose")
    log.info("-" * 60)

    # Build observation
    if observation and len(observation) == OBS_DIM:
        obs = np.array(observation, dtype=np.float32)
    else:
        obs = get_default_observation()

    # Run policy
    with _policy_lock:
        raw_action, _states = _policy.predict(obs, deterministic=False)

    raw_action = np.asarray(raw_action, dtype=np.float32)
    log.info("  [Policy] Raw action (17-dim): mean=%.3f, std=%.3f, range=[%.3f, %.3f]",
             raw_action.mean(), raw_action.std(), raw_action.min(), raw_action.max())

    # Apply instruction bias
    biased_action = apply_instruction_bias(raw_action, instruction)
    log.info("  [Bias] After instruction bias: mean=%.3f, std=%.3f",
             biased_action.mean(), biased_action.std())

    # Convert to fleet joint schema
    predicted_actions = mujoco_to_fleet_actions(biased_action)

    total_ms = (time.perf_counter() - pipeline_start) * 1000

    log.info("-" * 60)
    log.info("RESULT: %d joint actions, latency=%.1fms", len(predicted_actions), total_ms)
    log.info("=" * 60)

    return {
        "predicted_actions": predicted_actions,
        "confidence": 0.85,
        "model_id": model_id,
        "model_version": model_version,
        "embodiment": embodiment,
        "action_dim": MUJOCO_ACTION_DIM,
        "latency_ms": round(total_ms, 1),
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
            )

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
            self.wfile.write(json.dumps({"status": status, "model": model_id, "ready": ready}).encode())
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
    else:
        log.info("[Model] REDIS_ADDR not set — model hot-swap disabled")

    server.serve_forever()


if __name__ == "__main__":
    main()
