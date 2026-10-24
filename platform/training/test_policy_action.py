"""How a policy's outputs become joint position targets.

Shared by training, the test track and the robots (policy_action.py is a
byte-identical copy in playground/simulator), so a policy moves the joints the
same way everywhere it runs.
"""

import mujoco
import numpy as np
import pytest

import policy_action as pa
from fleetos_env import LAB_XML
from observation import NUM_ACTUATORS


def test_absolute_actions_are_the_joint_targets():
    action = np.linspace(-2.0, 2.0, NUM_ACTUATORS, dtype=np.float32)
    assert pa.to_joint_targets(action, pa.ABSOLUTE) == pytest.approx(action)


def test_residual_actions_offset_the_standing_pose():
    zeros, ones = np.zeros(NUM_ACTUATORS), np.ones(NUM_ACTUATORS)
    assert pa.to_joint_targets(zeros, pa.RESIDUAL) == pytest.approx(pa.STAND_POSE)
    assert pa.to_joint_targets(ones, pa.RESIDUAL) == pytest.approx(pa.STAND_POSE + pa.RESIDUAL_SCALE)
    assert pa.to_joint_targets(-ones, pa.RESIDUAL) == pytest.approx(pa.STAND_POSE - pa.RESIDUAL_SCALE)


def test_residual_actions_beyond_the_unit_box_are_clipped():
    # PPO samples Gaussian actions that can leave [-1, 1]; the offset never exceeds the scale.
    big = np.full(NUM_ACTUATORS, 3.0)
    assert pa.to_joint_targets(big, pa.RESIDUAL) == pytest.approx(pa.STAND_POSE + pa.RESIDUAL_SCALE)


def test_unknown_action_version_is_rejected():
    with pytest.raises(ValueError, match="action version 99"):
        pa.to_joint_targets(np.zeros(NUM_ACTUATORS), 99)


def test_joint_targets_convert_back_to_residual_actions():
    rng = np.random.default_rng(0)
    action = rng.uniform(-1.0, 1.0, NUM_ACTUATORS)
    assert pa.from_joint_targets(pa.to_joint_targets(action, pa.RESIDUAL)) == pytest.approx(action)


def test_joint_targets_beyond_the_residual_range_clip_to_the_unit_box():
    far = pa.STAND_POSE + 2 * pa.RESIDUAL_SCALE
    assert pa.from_joint_targets(far) == pytest.approx(np.ones(NUM_ACTUATORS))


def test_stand_pose_is_the_scenes_standing_keyframe():
    model = mujoco.MjModel.from_xml_path(str(LAB_XML))
    qpos = model.key("stand").qpos
    joint_qpos = qpos[model.jnt_qposadr[model.actuator_trnid[:NUM_ACTUATORS, 0]]]
    assert pa.STAND_POSE == pytest.approx(joint_qpos)
