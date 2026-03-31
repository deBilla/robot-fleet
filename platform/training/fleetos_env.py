"""
Custom Gymnasium environment wrapping our lab_room.xml MuJoCo model.

Matches the Humanoid-v4 reward/observation structure but uses the actual
lab environment that robots run in production. This ensures the trained
policy works in the same physics conditions as deployment.

Key differences from stock Humanoid-v4:
- Uses lab_room.xml (floor friction, walls, obstacles)
- 7x sub-stepping (0.003s timestep × 7 = 0.021s per control step)
- Same reward function as simulator: forward_vel + alive_bonus - control_cost
- Registers as "FleetOS-Humanoid-v1" in Gymnasium
"""

import os
from pathlib import Path

import gymnasium as gym
import mujoco
import numpy as np
from gymnasium import spaces
from gymnasium.envs.registration import register

# Paths
ASSETS_DIR = Path(__file__).parent / "assets"
LAB_XML = ASSETS_DIR / "lab_room.xml"

# Constants matching our simulator
NUM_ACTUATORS = 17
ROOT_QPOS = 7   # 3 pos + 4 quat
ROOT_QVEL = 6   # 3 lin vel + 3 ang vel
BODY_QPOS = ROOT_QPOS + NUM_ACTUATORS  # 24
BODY_QVEL = ROOT_QVEL + NUM_ACTUATORS  # 23
N_SUBSTEPS = 7  # matches simulator: 0.003s × 7 = 0.021s per step

# Reward constants (matching simulator mujoco_env.py)
ALIVE_BONUS = 5.0
CTRL_COST_WEIGHT = 0.1
HEALTHY_Z_MIN = 0.5
FALL_Z = 0.4


class FleetOSHumanoidEnv(gym.Env):
    """Gymnasium env using our lab_room.xml with Humanoid-v4 compatible API."""

    metadata = {"render_modes": ["rgb_array"], "render_fps": 50}

    def __init__(self, render_mode=None, max_episode_steps=1000):
        super().__init__()

        if not LAB_XML.exists():
            raise FileNotFoundError(f"Lab XML not found: {LAB_XML}")

        self.model = mujoco.MjModel.from_xml_path(str(LAB_XML))
        self.data = mujoco.MjData(self.model)
        self.render_mode = render_mode
        self._max_episode_steps = max_episode_steps
        self._step_count = 0

        # Action space: 17 actuators, range [-1, 1] (motor ctrllimited in XML)
        self.action_space = spaces.Box(
            low=-1.0, high=1.0, shape=(NUM_ACTUATORS,), dtype=np.float32
        )

        # Observation space: match Humanoid-v4 (376-dim)
        # We build a similar observation from our model's state
        obs_size = self._get_obs().shape[0]
        self.observation_space = spaces.Box(
            low=-np.inf, high=np.inf, shape=(obs_size,), dtype=np.float64
        )

        self._prev_x = 0.0

    def _get_obs(self) -> np.ndarray:
        """Build observation vector from MuJoCo state.

        Structure (matching Humanoid-v4 for policy compatibility):
        - qpos[2:] (exclude x, y — agent shouldn't know absolute position) — 22 dims
        - qvel (full) — 23 dims
        - cinert (body inertias flattened) — variable
        - cvel (body velocities flattened) — variable
        - qfrc_actuator — 23 dims
        - cfrc_ext (external forces) — variable
        """
        position = self.data.qpos[2:].flat.copy()
        velocity = self.data.qvel.flat.copy()
        com_inertia = self.data.cinert[1:].flat.copy()
        com_velocity = self.data.cvel[1:].flat.copy()
        actuator_forces = self.data.qfrc_actuator.flat.copy()
        external_forces = self.data.cfrc_ext[1:].flat.copy()

        return np.concatenate([
            position, velocity,
            com_inertia, com_velocity,
            actuator_forces, external_forces,
        ])

    def reset(self, *, seed=None, options=None):
        super().reset(seed=seed)
        mujoco.mj_resetData(self.model, self.data)

        # Standing pose at origin
        self.data.qpos[2] = 1.4  # standing height
        self.data.qpos[3] = 1.0  # quaternion w (upright)

        # Small noise for exploration
        noise = self.np_random.uniform(low=-0.005, high=0.005, size=self.model.nq)
        noise[:3] = 0  # don't perturb position
        noise[3:7] = 0  # don't perturb quaternion
        self.data.qpos[:] += noise[:self.model.nq]

        vel_noise = self.np_random.uniform(low=-0.005, high=0.005, size=self.model.nv)
        self.data.qvel[:] = vel_noise[:self.model.nv]

        mujoco.mj_forward(self.model, self.data)

        self._prev_x = self.data.qpos[0]
        self._step_count = 0

        return self._get_obs(), {}

    def step(self, action):
        action = np.clip(action, -1.0, 1.0)
        self.data.ctrl[:NUM_ACTUATORS] = action

        # Sub-step to match simulator real-time physics
        for _ in range(N_SUBSTEPS):
            mujoco.mj_step(self.model, self.data)

        self._step_count += 1

        obs = self._get_obs()
        height = self.data.qpos[2]
        x_pos = self.data.qpos[0]

        # Reward: same as simulator
        dt = self.model.opt.timestep * N_SUBSTEPS
        forward_velocity = (x_pos - self._prev_x) / dt
        self._prev_x = x_pos

        ctrl_cost = CTRL_COST_WEIGHT * np.sum(action ** 2)
        alive_bonus = ALIVE_BONUS if height > HEALTHY_Z_MIN else 0.0

        reward = forward_velocity + alive_bonus - ctrl_cost

        # Termination
        fallen = height < FALL_Z
        truncated = self._step_count >= self._max_episode_steps
        terminated = fallen

        info = {
            "reward_forward": forward_velocity,
            "reward_alive": alive_bonus,
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
        id="FleetOS-Humanoid-v1",
        entry_point="fleetos_env:FleetOSHumanoidEnv",
        max_episode_steps=1000,
    )
