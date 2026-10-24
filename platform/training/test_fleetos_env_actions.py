"""The test track maps actions to joint targets with the shared policy_action rules."""

import numpy as np
import pytest

import policy_action as pa
from fleetos_env import FleetOSHumanoidEnv
from locomotion_reward import control_cost
from observation import NUM_ACTUATORS


def _ctrl_range(env):
    return env.model.actuator_ctrlrange[:NUM_ACTUATORS, 0], env.model.actuator_ctrlrange[:NUM_ACTUATORS, 1]


def test_training_env_acts_in_residual_unit_box_by_default():
    env = FleetOSHumanoidEnv()
    assert env.action_space.shape == (NUM_ACTUATORS,)
    assert np.all(env.action_space.low == -1.0) and np.all(env.action_space.high == 1.0)


@pytest.mark.parametrize("action_value", [0.0, 1.0, -1.0])
def test_residual_action_drives_joints_around_the_standing_pose(action_value):
    env = FleetOSHumanoidEnv()
    env.reset(seed=0)
    low, high = _ctrl_range(env)
    action = np.full(NUM_ACTUATORS, action_value, dtype=np.float32)
    _, _, _, _, info = env.step(action)
    expected = np.clip(pa.to_joint_targets(action, pa.RESIDUAL), low, high)
    assert env.data.ctrl[:NUM_ACTUATORS] == pytest.approx(expected)
    # Effort is charged on the joint targets actually applied, as on the robots.
    assert info["reward_ctrl"] == pytest.approx(-control_cost(expected))


def test_absolute_env_takes_joint_targets_in_each_actuators_range():
    env = FleetOSHumanoidEnv(action_version=pa.ABSOLUTE)
    low, high = _ctrl_range(env)
    assert env.action_space.low == pytest.approx(low) and env.action_space.high == pytest.approx(high)
    env.reset(seed=0)
    action = np.full(NUM_ACTUATORS, -2.0, dtype=np.float32)
    env.step(action)
    assert env.data.ctrl[:NUM_ACTUATORS] == pytest.approx(np.clip(action, low, high))
