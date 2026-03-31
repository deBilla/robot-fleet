"""
Experience buffer that collects (obs, action, reward, done) transitions
and periodically flushes to S3 as NDJSON batches for offline RL training.

Builds Humanoid-v4-compatible observations from MuJoCo state so the
experience data matches the training environment's observation space.
"""

import json
import logging
import os
import threading
import time
from io import BytesIO
from pathlib import Path

import mujoco
import numpy as np

log = logging.getLogger(__name__)

BATCH_SIZE = 500       # transitions per S3 flush
FLUSH_INTERVAL = 60.0  # seconds between time-based flushes


class ExperienceWriter:
    """Collects RL transitions and writes to S3 as NDJSON batches."""

    def __init__(self, s3_endpoint: str = "", s3_bucket: str = "fleetos-models",
                 s3_access_key: str = "fleetos", s3_secret_key: str = "fleetos123"):
        self._buffer: list[dict] = []
        self._lock = threading.Lock()
        self._s3_client = None
        self._bucket = s3_bucket
        self._total_written = 0
        self._last_flush = time.time()

        if s3_endpoint:
            try:
                from minio import Minio
                self._s3_client = Minio(
                    s3_endpoint, access_key=s3_access_key,
                    secret_key=s3_secret_key, secure=False,
                )
                log.info("Experience writer connected to S3: %s/%s", s3_endpoint, s3_bucket)
            except Exception as e:
                log.warning("Experience writer S3 not available: %s", e)

    def record(self, robot_id: str, model: mujoco.MjModel, data: mujoco.MjData,
               action: np.ndarray, reward: float, done: bool,
               qpos_start: int = 0, qvel_start: int = 0, act_start: int = 0,
               num_actuators: int = 17):
        """Record a single transition.

        Builds the observation from MuJoCo state (matching FleetOS-Humanoid-v1 env).
        """
        obs = self._build_observation(data, qpos_start, qvel_start)

        transition = {
            "robot_id": robot_id,
            "obs": obs.tolist(),
            "action": action.tolist() if hasattr(action, 'tolist') else list(action),
            "reward": round(float(reward), 4),
            "done": bool(done),
            "ts": time.time(),
        }

        with self._lock:
            self._buffer.append(transition)

            try:
                if len(self._buffer) >= BATCH_SIZE:
                    self._flush()
                elif time.time() - self._last_flush > FLUSH_INTERVAL:
                    self._flush()
            except Exception as e:
                log.error("Experience flush error: %s", e)

    def _build_observation(self, data: mujoco.MjData,
                           qpos_start: int, qvel_start: int) -> np.ndarray:
        """Build Humanoid-v4-compatible observation from MuJoCo state.

        Matches fleetos_env.py FleetOSHumanoidEnv._get_obs():
        [qpos[2:], qvel, cinert[1:], cvel[1:], qfrc_actuator, cfrc_ext[1:]]
        """
        position = data.qpos[qpos_start + 2:].flat.copy()
        velocity = data.qvel[qvel_start:].flat.copy()
        com_inertia = data.cinert[1:].flat.copy()
        com_velocity = data.cvel[1:].flat.copy()
        actuator_forces = data.qfrc_actuator.flat.copy()
        external_forces = data.cfrc_ext[1:].flat.copy()

        return np.concatenate([
            position, velocity,
            com_inertia, com_velocity,
            actuator_forces, external_forces,
        ]).astype(np.float32)

    def _flush(self):
        """Write buffered transitions to S3 as NDJSON."""
        if not self._buffer:
            return

        batch = self._buffer.copy()
        self._buffer.clear()
        self._last_flush = time.time()

        if not self._s3_client:
            log.debug("Experience batch discarded (no S3): %d transitions", len(batch))
            return

        # Build NDJSON
        lines = []
        for t in batch:
            lines.append(json.dumps(t, separators=(',', ':')))
        ndjson = "\n".join(lines).encode()

        # S3 key: experience/{date}/{robot_id}/batch_{timestamp}.ndjson
        now = time.strftime("%Y/%m/%d/%H")
        robot_id = batch[0].get("robot_id", "unknown")
        key = f"experience/{now}/{robot_id}/batch_{int(time.time() * 1000)}.ndjson"

        try:
            self._s3_client.put_object(
                self._bucket, key, BytesIO(ndjson), len(ndjson),
                content_type="application/x-ndjson",
            )
            self._total_written += len(batch)
            log.info("Experience batch written: %s (%d transitions, total: %d)",
                     key, len(batch), self._total_written)
        except Exception as e:
            log.error("Failed to write experience batch: %s", e)

    def close(self):
        """Flush remaining transitions."""
        with self._lock:
            self._flush()
        log.info("Experience writer closed: %d total transitions written", self._total_written)
