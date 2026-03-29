"""
Redis command subscriber and command-to-MuJoCo-action translator.

Commands arrive as JSON on Redis channel `commands:{robot_id}`:
  {"robot_id": "robot-0001", "command": {"type": "move", "params": {...}}}

Translates commands into 17-dim MuJoCo torque arrays.
"""

import json
import logging
import math
import threading
from typing import Optional

import numpy as np
import redis

from joint_mapping import FLEET_TO_MUJOCO_ACTUATOR

log = logging.getLogger(__name__)

NUM_ACTUATORS = 17


class CommandHandler:
    """Subscribes to Redis commands and produces MuJoCo actions."""

    def __init__(self, redis_addr: str, robot_id: str):
        self.robot_id = robot_id
        self.current_command = "idle"
        self.command_params: dict = {}
        self.command_tick = 0
        self._lock = threading.Lock()

        host, port = redis_addr.split(":")
        self.client = redis.Redis(host=host, port=int(port), decode_responses=True)
        self._thread: Optional[threading.Thread] = None

    def start(self):
        """Start the Redis subscription in a background thread."""
        self._thread = threading.Thread(target=self._subscribe, daemon=True)
        self._thread.start()
        log.info("Command handler started for %s", self.robot_id)

    def _subscribe(self):
        pubsub = self.client.pubsub()
        channel = f"commands:{self.robot_id}"
        pubsub.subscribe(channel)
        log.info("Subscribed to %s", channel)

        for message in pubsub.listen():
            if message["type"] != "message":
                continue
            try:
                data = json.loads(message["data"])
                cmd = data.get("command", {})
                cmd_type = cmd.get("type", "")
                params = cmd.get("params", {})

                with self._lock:
                    self.current_command = cmd_type
                    self.command_params = params
                    self.command_tick = 0

                log.info("Command received: %s params=%s", cmd_type, params)
            except Exception:
                log.exception("Failed to parse command")

    def get_action(self, step: int) -> np.ndarray:
        """Produce a 17-dim action array based on current command.

        Args:
            step: Current simulation step (for time-varying actions).

        Returns:
            np.ndarray of shape (17,) with values in [-1, 1].
        """
        with self._lock:
            cmd = self.current_command
            params = self.command_params
            self.command_tick += 1
            tick = self.command_tick

        if cmd == "stop":
            return np.zeros(NUM_ACTUATORS, dtype=np.float32)

        if cmd == "apply_actions":
            return self._apply_inference_actions(params)

        if cmd in ("move", "move_relative", "walk"):
            return self._walk_action(step)

        if cmd == "dance":
            return self._dance_action(step)

        if cmd == "wave":
            return self._wave_action(step)

        if cmd == "jump":
            return self._jump_action(step)

        if cmd == "bow":
            return self._bow_action(step)

        if cmd in ("sit", "crouch"):
            return self._sit_action(step)

        if cmd == "look_around":
            return self._look_action(step)

        if cmd == "stretch":
            return self._stretch_action(step)

        # idle: small stabilization torques
        return self._idle_action(step)

    def _apply_inference_actions(self, params: dict) -> np.ndarray:
        """Apply joint torques from inference predicted_actions directly."""
        action = self._idle_action(0)  # start from stable base
        actions = params.get("actions", [])
        for a in actions:
            joint_name = a.get("joint", "")
            torque = a.get("torque", 0.0)
            if joint_name in FLEET_TO_MUJOCO_ACTUATOR:
                idx = FLEET_TO_MUJOCO_ACTUATOR[joint_name]
                action[idx] = np.clip(torque, -1.0, 1.0)
        return action

    def _idle_action(self, step: int) -> np.ndarray:
        """Stabilization torques to keep the humanoid balanced while standing.

        MuJoCo Humanoid-v4 is inherently unstable. These torques provide
        a standing equilibrium by stiffening the core and legs.
        """
        action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        # Core stiffness
        action[1] = 0.15   # abdomen_y: lean forward to counteract backward fall
        action[2] = 0.05   # abdomen_x: lateral stability

        # Leg stiffness: slight knee/hip extension to stay upright
        action[5] = 0.1    # right_hip_y
        action[9] = 0.1    # left_hip_y
        action[6] = -0.15  # right_knee (extend)
        action[10] = -0.15 # left_knee (extend)

        # Hip roll centering
        action[3] = 0.05   # right_hip_x
        action[7] = -0.05  # left_hip_x

        return action

    def _walk_action(self, step: int) -> np.ndarray:
        """Cyclic hip/knee torques to produce forward walking gait."""
        action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        t = step * 0.02  # time in seconds (50 Hz)
        phase = t * 1.8 * 2 * math.pi  # 1.8 Hz gait

        # Hip pitch: alternating push
        action[5] = 0.6 * math.sin(phase)       # right_hip_y
        action[9] = -0.6 * math.sin(phase)      # left_hip_y

        # Knee extension during stance
        action[6] = 0.3 * max(0, math.sin(phase))    # right_knee
        action[10] = 0.3 * max(0, -math.sin(phase))  # left_knee

        # Hip roll for lateral balance
        action[3] = 0.15 * math.sin(phase * 0.5)  # right_hip_x
        action[7] = -0.15 * math.sin(phase * 0.5) # left_hip_x

        # Arm swing (counter to legs)
        action[11] = -0.3 * math.sin(phase)  # right_shoulder1
        action[14] = 0.3 * math.sin(phase)   # left_shoulder1

        # Torso stability
        action[1] = 0.1  # abdomen_y lean forward

        return action

    def _dance_action(self, step: int) -> np.ndarray:
        """Rhythmic full-body movement."""
        action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        t = step * 0.02
        phase = t * 2.5 * 2 * math.pi  # faster rhythm

        # Body sway
        action[0] = 0.4 * math.sin(phase)       # abdomen_z (twist)
        action[1] = 0.2 * math.sin(phase * 2)   # abdomen_y (bounce)

        # Arm movements
        action[11] = 0.5 * math.sin(phase)       # right_shoulder
        action[14] = 0.5 * math.sin(phase + 1)   # left_shoulder
        action[13] = 0.3 * math.sin(phase * 2)   # right_elbow
        action[16] = 0.3 * math.sin(phase * 2 + 1)  # left_elbow

        # Leg bounce
        action[5] = 0.3 * math.sin(phase)   # right_hip_y
        action[9] = 0.3 * math.sin(phase)   # left_hip_y
        action[6] = 0.2 * abs(math.sin(phase))   # right_knee
        action[10] = 0.2 * abs(math.sin(phase))  # left_knee

        return action

    def _wave_action(self, step: int) -> np.ndarray:
        """Wave the right arm."""
        action = self._idle_action(step)
        t = step * 0.02
        phase = t * 3 * 2 * math.pi

        action[11] = -0.7   # right shoulder up
        action[12] = 0.3    # right shoulder out
        action[13] = 0.5 * math.sin(phase)  # right elbow wave

        return action

    def _jump_action(self, step: int) -> np.ndarray:
        """Attempt a jump by extending legs forcefully."""
        action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        tick = self.command_tick

        if tick < 25:  # crouch phase (0.5s)
            action[5] = -0.8   # right_hip_y flex
            action[9] = -0.8   # left_hip_y flex
            action[6] = 0.8    # right_knee bend
            action[10] = 0.8   # left_knee bend
        elif tick < 35:  # extend phase (0.2s)
            action[5] = 0.9    # right_hip_y extend
            action[9] = 0.9    # left_hip_y extend
            action[6] = -0.9   # right_knee extend
            action[10] = -0.9  # left_knee extend
        else:  # land / stabilize
            action[1] = 0.1
            if tick > 75:
                with self._lock:
                    self.current_command = "idle"

        return action

    def _bow_action(self, step: int) -> np.ndarray:
        """Bow forward from the waist."""
        action = self._idle_action(step)
        tick = self.command_tick

        bow_amount = min(1.0, tick / 50) if tick < 50 else max(0.0, 1.0 - (tick - 75) / 50)
        action[1] = 0.5 * bow_amount  # abdomen_y forward
        action[5] = -0.2 * bow_amount  # slight hip flex
        action[9] = -0.2 * bow_amount

        if tick > 125:
            with self._lock:
                self.current_command = "idle"

        return action

    def _sit_action(self, step: int) -> np.ndarray:
        """Bend knees to sit."""
        action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        sit = min(1.0, self.command_tick / 50)

        action[5] = -0.6 * sit   # hip flex
        action[9] = -0.6 * sit
        action[6] = 0.7 * sit    # knee bend
        action[10] = 0.7 * sit
        action[1] = 0.15 * sit   # lean forward for balance

        return action

    def _look_action(self, step: int) -> np.ndarray:
        """Pan head left and right."""
        action = self._idle_action(step)
        t = step * 0.02
        action[0] = 0.4 * math.sin(t * 0.8 * 2 * math.pi)  # abdomen_z (head pan proxy)
        action[1] = 0.1 * math.sin(t * 0.4 * 2 * math.pi)   # slight nod

        if self.command_tick > 200:
            with self._lock:
                self.current_command = "idle"

        return action

    def _stretch_action(self, step: int) -> np.ndarray:
        """Arms up stretch."""
        action = self._idle_action(step)
        t = step * 0.02
        stretch = min(1.0, self.command_tick / 50)

        # Arms overhead
        action[11] = -0.7 * stretch  # right shoulder up
        action[14] = -0.7 * stretch  # left shoulder up
        action[13] = -0.3 * stretch  # elbows slightly bent
        action[16] = -0.3 * stretch

        # Slight side lean
        action[0] = 0.2 * math.sin(t * 0.5 * 2 * math.pi) * stretch

        if self.command_tick > 200:
            with self._lock:
                self.current_command = "idle"

        return action

    def close(self):
        self.client.close()
