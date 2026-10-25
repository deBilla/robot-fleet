"""
Custom Gymnasium environment wrapping our lab_room.xml MuJoCo model.

Uses the unified 86-dim observation format (observation.py) and
task-conditional reward matching the simulator.

Registers as "FleetOS-Humanoid-v2" in Gymnasium.
"""

import os
from pathlib import Path

import gymnasium as gym
import mujoco
import numpy as np
from gymnasium import spaces
from gymnasium.envs.registration import register

from observation import build_observation_from_mujoco, OBS_DIM, NUM_ACTUATORS
from locomotion_reward import MAX_EPISODE_STEPS, control_cost, has_fallen, locomotion_reward
from policy_action import RESIDUAL, to_joint_targets

ASSETS_DIR = Path(__file__).parent / "assets"
LAB_XML = ASSETS_DIR / "lab_room.xml"

N_SUBSTEPS = 7  # 0.003s × 7 = 0.021s per step

# Domain randomisation ranges (issue #17: sim-to-real robustness).
# Per-episode randomisation is opt-in via the `domain_randomisation` flag so
# that evaluation stays deterministic.
FRIC_MIN, FRIC_MAX = 0.7, 1.3          # sliding friction ×[0.7, 1.3]  (±30%)
MASS_MIN, MASS_MAX = 0.9, 1.1          # link mass ×[0.9, 1.1]         (±10%)
LATENCY_MS_MIN, LATENCY_MS_MAX = 10, 50  # action latency per command (ms)
OBS_NOISE_STD = 0.01                   # observation Gaussian noise std
# Bodies whose link mass is left untouched (static scenery).
_STATIC_SCENERY = {"world", "table", "shelf"}


class FleetOSHumanoidEnv(gym.Env):
    """Gymnasium env using our lab_room.xml with Humanoid-v4 compatible API."""

    metadata = {"render_modes": ["rgb_array"], "render_fps": 50}

    def __init__(self, render_mode=None, max_episode_steps=MAX_EPISODE_STEPS,
                 action_version=RESIDUAL, domain_randomisation=False):
        super().__init__()

        if not LAB_XML.exists():
            raise FileNotFoundError(f"Lab XML not found: {LAB_XML}")

        self.model = mujoco.MjModel.from_xml_path(str(LAB_XML))
        self.data = mujoco.MjData(self.model)
        self.render_mode = render_mode
        self._max_episode_steps = max_episode_steps
        self._step_count = 0
        self._domain_randomisation = domain_randomisation

        # Find robot root joint (after dynamic object freejoints)
        self._root_jnt_id = mujoco.mj_name2id(self.model, mujoco.mjtObj.mjOBJ_JOINT, "root")
        self._root_qpos_adr = self.model.jnt_qposadr[self._root_jnt_id]
        self._root_dof_adr = self.model.jnt_dofadr[self._root_jnt_id]

        # Actions map to joint position targets per policy_action.py; physics
        # applies each actuator's ctrlrange. Training uses RESIDUAL; the test
        # track evaluates older ABSOLUTE policies in their own version.
        self.action_version = action_version
        self._ctrl_low = self.model.actuator_ctrlrange[:, 0].astype(np.float32)
        self._ctrl_high = self.model.actuator_ctrlrange[:, 1].astype(np.float32)
        if action_version == RESIDUAL:
            self.action_space = spaces.Box(low=-1.0, high=1.0, shape=(NUM_ACTUATORS,), dtype=np.float32)
        else:
            self.action_space = spaces.Box(low=self._ctrl_low, high=self._ctrl_high, dtype=np.float32)

        # Unified 86-dim observation
        self.observation_space = spaces.Box(
            low=-np.inf, high=np.inf, shape=(OBS_DIM,), dtype=np.float32
        )

        self._prev_x = 0.0

        # Scenario conditions (evaluate_policy.SCENARIO_PRESETS); scene defaults until set.
        self._torso_id = self.model.body("torso").id
        self._base_sliding_friction = self.model.geom_friction[:, 0].copy()
        self._base_mass = self.model.body_mass.copy()
        self._wind = 0.0
        # Scenario-adjusted physics base: starts at the scene defaults and is
        # rewritten by set_conditions. Domain randomisation scales this base
        # each episode (never compounds across resets).
        self._current_friction_base = self._base_sliding_friction.copy()
        self._current_mass_base = self._base_mass.copy()

        # Bodies whose mass is randomised (everything except static scenery).
        self._randomisable_bodies = [
            i for i in range(self.model.nbody)
            if mujoco.mj_id2name(self.model, mujoco.mjtObj.mjOBJ_BODY, i) not in _STATIC_SCENERY
        ]

        # Action latency buffering: the most recently issued command takes
        # effect after a random delay (10-50 ms). Until then the previously
        # applied command keeps being sent to the actuators.
        self._applied_action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        self._pending_action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        self._latency_substeps = 0  # physics substeps until _pending_action applies

    def set_conditions(self, gravity: float, friction: float, wind: float):
        """Physics for a test-track scenario, kept across resets.

        gravity: vertical acceleration (m/s², negative is down).
        friction: multiplier on every geom's sliding friction from the scene.
        wind: constant lateral force on the torso (N, +y: a side gust).
        """
        self.model.opt.gravity[:] = (0.0, 0.0, gravity)
        self.model.geom_friction[:, 0] = self._base_sliding_friction * friction
        self._current_friction_base = self._base_sliding_friction * friction
        self._wind = wind
        self.data.xfrc_applied[self._torso_id, :3] = (0.0, wind, 0.0)

    def _randomise_physics(self):
        """Re-randomise friction and link mass for a new episode.

        Scales the *current* physics (so it composes on top of
        `set_conditions` presets) and only when the flag is enabled.
        """
        if not self._domain_randomisation:
            return
        self.model.geom_friction[:, 0] = self._current_friction_base * self.np_random.uniform(
            FRIC_MIN, FRIC_MAX, size=self.model.geom_friction[:, 0].shape
        )
        self.model.body_mass[self._randomisable_bodies] = (
            self._current_mass_base[self._randomisable_bodies]
            * self.np_random.uniform(MASS_MIN, MASS_MAX, size=len(self._randomisable_bodies))
        )

    def _draw_latency_substeps(self) -> int:
        """Random action latency for the next issued command, in physics substeps."""
        latency_ms = self.np_random.uniform(LATENCY_MS_MIN, LATENCY_MS_MAX)
        sub_ms = self.model.opt.timestep * 1000.0  # milliseconds per substep
        return max(1, int(round(latency_ms / sub_ms)))

    def _get_obs(self) -> np.ndarray:
        """Build unified 86-dim observation from MuJoCo state."""
        obs = build_observation_from_mujoco(
            self.model, self.data,
            self._root_qpos_adr, self._root_dof_adr,
            task_command="walk",
        )
        if self._domain_randomisation:
            obs = obs + self.np_random.normal(0.0, OBS_NOISE_STD, size=obs.shape)
        return obs.astype(np.float32)

    def reset(self, *, seed=None, options=None):
        super().reset(seed=seed)
        mujoco.mj_resetData(self.model, self.data)

        qs = self._root_qpos_adr
        self.data.qpos[qs + 2] = 1.22  # standing height
        self.data.qpos[qs + 3] = 1.0   # quaternion w (upright)

        # Small noise on joints only
        joint_start = qs + 7
        joint_end = joint_start + NUM_ACTUATORS
        noise = self.np_random.uniform(-0.01, 0.01, size=NUM_ACTUATORS)
        self.data.qpos[joint_start:joint_end] += noise

        # Per-episode domain randomisation layers on top of the scene defaults
        # (or on top of `set_conditions` presets applied before reset).
        self._randomise_physics()

        # mj_resetData clears applied forces; wind persists across episodes.
        self.data.xfrc_applied[self._torso_id, :3] = (0.0, self._wind, 0.0)

        # Reset action-latency buffering so each episode starts clean.
        self._applied_action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        self._pending_action = np.zeros(NUM_ACTUATORS, dtype=np.float32)
        self._latency_substeps = 0

        mujoco.mj_forward(self.model, self.data)

        self._prev_x = self.data.qpos[qs]
        self._step_count = 0

        return self._get_obs(), {}

    def step(self, action):
        # A new command is issued; it takes effect after a random action
        # latency (10-50 ms). Until then the previously applied command is
        # re-sent to the actuators.
        self._pending_action = np.clip(to_joint_targets(action, self.action_version),
                                       self._ctrl_low, self._ctrl_high)

        n_old = 0
        if self._domain_randomisation and self._latency_substeps > 0:
            # Still waiting for the current command to take effect: keep
            # applying the previous action for the remaining latency substeps.
            n_old = min(self._latency_substeps, N_SUBSTEPS)
            self._latency_substeps -= n_old
            self.data.ctrl[:NUM_ACTUATORS] = self._applied_action
            for _ in range(n_old):
                mujoco.mj_step(self.model, self.data)

        remaining = N_SUBSTEPS - n_old
        if remaining > 0:
            # The pending command takes effect for the rest of this step.
            self.data.ctrl[:NUM_ACTUATORS] = self._pending_action
            self._applied_action = self._pending_action.copy()
            for _ in range(remaining):
                mujoco.mj_step(self.model, self.data)

        if self._domain_randomisation and self._latency_substeps == 0:
            # Draw latency for the next issued command.
            self._latency_substeps = self._draw_latency_substeps()

        self._step_count += 1

        obs = self._get_obs()
        qs = self._root_qpos_adr
        height = self.data.qpos[qs + 2]
        x_pos = self.data.qpos[qs]

        # Locomotion reward (matches simulator task-conditional reward for "walk")
        dt = self.model.opt.timestep * N_SUBSTEPS
        forward_velocity = (x_pos - self._prev_x) / dt
        self._prev_x = x_pos

        ctrl_cost = control_cost(self._applied_action)
        # Shared with the robots (locomotion_reward.py) so the test track
        # scores and terminates exactly like the fleet.
        reward = locomotion_reward(forward_velocity, height, ctrl_cost)
        terminated = has_fallen(height)
        truncated = self._step_count >= self._max_episode_steps

        info = {
            "reward_forward": forward_velocity,
            "reward_ctrl": -ctrl_cost,
            "height": height,
            "x_position": x_pos,
        }

        return obs, reward, terminated, truncated, info

    def render(self):
        if self.render_mode == "rgb_array":
            renderer = mujoco.Renderer(self.model, 480, 640)
            renderer.update_scene(self.data)
            return renderer.render()
        return None

    def close(self):
        pass


def register_fleetos_env():
    """Register the FleetOS Humanoid environment with Gymnasium."""
    register(
        id="FleetOS-Humanoid-v2",
        entry_point="fleetos_env:FleetOSHumanoidEnv",
        max_episode_steps=MAX_EPISODE_STEPS,
    )
