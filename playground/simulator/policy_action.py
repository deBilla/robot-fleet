"""How a policy's outputs become joint position targets.

Byte-identical copies live in platform/training (training env, test track,
offline trainer) and playground/simulator (robots); platform/training/
test_parity.py fails CI if they drift, and platform/inference/server.py mirrors
the constants. Every policy records its action version in policy_meta.json.

ABSOLUTE: the action is the joint targets, anywhere in each actuator's range.
RESIDUAL: the action is in [-1, 1] and offsets each joint from the standing
pose by up to RESIDUAL_SCALE. Policies learn to balance with small corrections
far more easily than with full-range targets (platform/docs/locomotion-research.md).

Fleet experience always records joint targets, whatever produced them.
"""

import numpy as np

from observation import NUM_ACTUATORS

ABSOLUTE = 1
RESIDUAL = 2
SUPPORTED_ACTION_VERSIONS = (ABSOLUTE, RESIDUAL)

RESIDUAL_SCALE = 0.5      # rad: largest offset from the standing pose a residual action commands
STAND_JOINT_ANGLE = 0.0   # rad: every joint's angle in lab_room.xml's "stand" keyframe
STAND_POSE = np.full(NUM_ACTUATORS, STAND_JOINT_ANGLE, dtype=np.float32)


def to_joint_targets(action, action_version: int) -> np.ndarray:
    """Joint position targets for a policy action (before actuator range limits)."""
    action = np.asarray(action, dtype=np.float32)
    if action_version == ABSOLUTE:
        return action
    if action_version == RESIDUAL:
        return STAND_POSE + RESIDUAL_SCALE * np.clip(action, -1.0, 1.0)
    raise ValueError(f"unknown action version {action_version}")


def from_joint_targets(targets) -> np.ndarray:
    """The residual action closest to producing these joint targets."""
    offsets = (np.asarray(targets, dtype=np.float32) - STAND_POSE) / RESIDUAL_SCALE
    return np.clip(offsets, -1.0, 1.0)
