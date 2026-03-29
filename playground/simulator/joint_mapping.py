"""
Maps MuJoCo Humanoid-v4 joints to FleetOS 20-joint schema.

MuJoCo Humanoid-v4 has 17 actuated joints. Our schema has 20.
The 4 ankle joints (left/right ankle pitch/roll) have no MuJoCo equivalent
and are reported as zero.
"""

# MuJoCo Humanoid-v4 actuator index -> our joint name
# Actuator order matches env.action_space (17 dims)
MUJOCO_ACTUATOR_TO_FLEET = {
    0: "head_pan",               # abdomen_z
    1: "head_tilt",              # abdomen_y
    2: None,                     # abdomen_x (no mapping)
    3: "right_hip_roll",         # right_hip_x
    4: "right_hip_yaw",          # right_hip_z
    5: "right_hip_pitch",        # right_hip_y
    6: "right_knee",             # right_knee
    7: "left_hip_roll",          # left_hip_x
    8: "left_hip_yaw",           # left_hip_z
    9: "left_hip_pitch",         # left_hip_y
    10: "left_knee",             # left_knee
    11: "right_shoulder_pitch",  # right_shoulder1
    12: "right_shoulder_roll",   # right_shoulder2
    13: "right_elbow",           # right_elbow
    14: "left_shoulder_pitch",   # left_shoulder1
    15: "left_shoulder_roll",    # left_shoulder2
    16: "left_elbow",            # left_elbow
}

# Reverse: our joint name -> MuJoCo actuator index
FLEET_TO_MUJOCO_ACTUATOR = {
    name: idx for idx, name in MUJOCO_ACTUATOR_TO_FLEET.items() if name is not None
}

# MuJoCo qpos indices for joints (after the 7 root body entries)
# qpos[0:3] = root position (x, y, z)
# qpos[3:7] = root quaternion (w, x, y, z)
# qpos[7:24] = 17 joint angles
MUJOCO_QPOS_OFFSET = 7

# MuJoCo qvel indices for joints (after the 6 root body entries)
# qvel[0:3] = root linear velocity
# qvel[3:6] = root angular velocity
# qvel[6:23] = 17 joint velocities
MUJOCO_QVEL_OFFSET = 6

# Our full 20-joint schema (matching playground/internal/simulator/robot.go)
FLEET_JOINT_NAMES = [
    "head_pan", "head_tilt",
    "left_shoulder_pitch", "left_shoulder_roll", "left_elbow",
    "right_shoulder_pitch", "right_shoulder_roll", "right_elbow",
    "left_hip_yaw", "left_hip_roll", "left_hip_pitch", "left_knee",
    "left_ankle_pitch", "left_ankle_roll",
    "right_hip_yaw", "right_hip_roll", "right_hip_pitch", "right_knee",
    "right_ankle_pitch", "right_ankle_roll",
]

# Joints that exist only in our schema, not in MuJoCo
UNMAPPED_JOINTS = {"left_ankle_pitch", "left_ankle_roll",
                   "right_ankle_pitch", "right_ankle_roll"}


def extract_joint_states(qpos, qvel, actuator_force):
    """Convert MuJoCo state arrays to a list of (name, position, velocity, torque) tuples.

    Args:
        qpos: Full qpos array (24 entries for Humanoid-v4)
        qvel: Full qvel array (23 entries)
        actuator_force: Actuator forces (17 entries)

    Returns:
        List of (joint_name, position_rad, velocity_rad_s, torque_nm) for all 20 joints.
    """
    states = []
    for name in FLEET_JOINT_NAMES:
        if name in UNMAPPED_JOINTS:
            states.append((name, 0.0, 0.0, 0.0))
            continue

        act_idx = FLEET_TO_MUJOCO_ACTUATOR[name]
        pos = float(qpos[MUJOCO_QPOS_OFFSET + act_idx])
        vel = float(qvel[MUJOCO_QVEL_OFFSET + act_idx])
        torque = float(actuator_force[act_idx]) if actuator_force is not None else 0.0
        states.append((name, pos, vel, torque))

    return states


def extract_root_pose(qpos):
    """Extract root body position and orientation from qpos.

    MuJoCo Humanoid-v4 uses Z-up. Position is (x, y, z) in meters.
    Orientation is quaternion (w, x, y, z).

    Returns:
        (pos_x, pos_y, pos_z, quat_x, quat_y, quat_z, quat_w)
    """
    x, y, z = float(qpos[0]), float(qpos[1]), float(qpos[2])
    qw, qx, qy, qz = float(qpos[3]), float(qpos[4]), float(qpos[5]), float(qpos[6])
    return x, y, z, qx, qy, qz, qw
