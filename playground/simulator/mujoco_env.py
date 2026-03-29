"""
MuJoCo physics simulation with custom lab room environment.

Uses the direct mujoco Python API (not gymnasium) for full control
over the scene and dynamic robot spawning. Rendering is done
client-side via Three.js.
"""

import logging
import os
from pathlib import Path

import mujoco
import numpy as np

from joint_mapping import extract_joint_states, extract_root_pose, MUJOCO_QPOS_OFFSET

log = logging.getLogger(__name__)

ASSETS_DIR = Path(__file__).parent / "assets"
LAB_XML = ASSETS_DIR / "lab_room.xml"

# Humanoid body constants
NUM_ACTUATORS = 17
ROOT_QPOS_SIZE = 7   # 3 pos + 4 quat
ROOT_QVEL_SIZE = 6   # 3 lin vel + 3 ang vel
JOINT_QPOS_SIZE = 17
JOINT_QVEL_SIZE = 17
BODY_QPOS_SIZE = ROOT_QPOS_SIZE + JOINT_QPOS_SIZE  # 24
BODY_QVEL_SIZE = ROOT_QVEL_SIZE + JOINT_QVEL_SIZE   # 23



class LabSimulation:
    """Manages the MuJoCo lab room with one or more humanoid robots."""

    def __init__(self):
        self._base_xml = LAB_XML.read_text()
        self.robot_count = 1
        self.robots = []
        self.steps = 0
        self._load_model(self._build_xml(1))

    def _load_model(self, xml_string: str):
        """Load or reload the MuJoCo model from an XML string."""
        self.model = mujoco.MjModel.from_xml_string(xml_string)
        self.data = mujoco.MjData(self.model)

        # Rebuild robot tracking from model state
        self.robots = []
        for i in range(self.robot_count):
            self.robots.append({
                "id": f"robot-{i+1:04d}",
                "qpos_start": i * BODY_QPOS_SIZE,
                "qvel_start": i * BODY_QVEL_SIZE,
                "actuator_start": i * NUM_ACTUATORS,
                "battery": 1.0,
                "status": "active",
            })

        mujoco.mj_step(self.model, self.data)
        log.info("Model loaded: %d robots, %d actuators, %d qpos",
                 len(self.robots), self.model.nu, self.model.nq)

    def _build_xml(self, count: int) -> str:
        """Build XML string with `count` humanoid bodies.

        The base XML has 1 humanoid. For additional robots, we duplicate
        the humanoid body/tendon/actuator blocks with unique names and
        different spawn positions.
        """
        if count <= 1:
            return self._base_xml

        import re

        xml = self._base_xml
        # Extract the humanoid body block (between torso body and </worldbody>)
        body_match = re.search(
            r'(<!-- ===== Humanoid Robot.*?)(<!-- ===== Cameras)',
            xml, re.DOTALL
        )
        if not body_match:
            # Fallback: find the torso body directly
            body_match = re.search(
                r'(<body name="torso".*?</body>\s*</body>\s*</body>\s*</body>\s*</body>)',
                xml, re.DOTALL
            )

        tendon_match = re.search(r'(<tendon>.*?</tendon>)', xml, re.DOTALL)
        actuator_match = re.search(r'(<actuator>.*?</actuator>)', xml, re.DOTALL)

        if not tendon_match or not actuator_match:
            log.error("Could not parse XML for multi-robot spawn")
            return xml

        tendon_block = tendon_match.group(1)
        actuator_block = actuator_match.group(1)

        # Spawn positions: spread around the room
        positions = [
            (0, 0),      # robot 1 (already in XML)
            (2, 2),      # robot 2
            (-2, 2),     # robot 3
            (2, -2),     # robot 4
            (-2, -2),    # robot 5
            (0, 3),      # robot 6
            (3, 0),      # robot 7
            (-3, 0),     # robot 8
        ]

        extra_bodies = ""
        extra_tendons = ""
        extra_actuators = ""

        for i in range(1, count):
            suffix = f"_{i+1}"
            px, py = positions[i] if i < len(positions) else (i * 1.5 % 8 - 4, i * 1.3 % 8 - 4)

            # Duplicate the torso body with renamed joints/geoms and new position
            body = self._make_humanoid_body(suffix, px, py)
            extra_bodies += body + "\n"

            # Duplicate tendons
            extra_tendons += f"""
    <fixed name="left_hipknee{suffix}">
        <joint coef="-1" joint="left_hip_y{suffix}"/>
        <joint coef="1" joint="left_knee{suffix}"/>
    </fixed>
    <fixed name="right_hipknee{suffix}">
        <joint coef="-1" joint="right_hip_y{suffix}"/>
        <joint coef="1" joint="right_knee{suffix}"/>
    </fixed>"""

            # Duplicate actuators
            actuator_names = [
                "abdomen_y", "abdomen_z", "abdomen_x",
                "right_hip_x", "right_hip_z", "right_hip_y", "right_knee",
                "left_hip_x", "left_hip_z", "left_hip_y", "left_knee",
                "right_shoulder1", "right_shoulder2", "right_elbow",
                "left_shoulder1", "left_shoulder2", "left_elbow",
            ]
            gears = [100, 100, 100, 100, 100, 300, 200, 100, 100, 300, 200, 25, 25, 25, 25, 25, 25]
            for name, gear in zip(actuator_names, gears):
                extra_actuators += f'        <motor gear="{gear}" joint="{name}{suffix}" name="{name}{suffix}"/>\n'

        # Insert extra bodies before cameras
        xml = xml.replace(
            "<!-- ===== Cameras =====",
            extra_bodies + "\n        <!-- ===== Cameras ====="
        )

        # Insert extra tendons
        xml = xml.replace("</tendon>", extra_tendons + "\n    </tendon>")

        # Insert extra actuators
        xml = xml.replace("</actuator>", extra_actuators + "    </actuator>")

        return xml

    def _make_humanoid_body(self, suffix: str, px: float, py: float) -> str:
        """Generate a humanoid body block with renamed joints and position offset."""
        # This is the humanoid body from the base XML with all names suffixed
        return f"""
        <body name="torso{suffix}" pos="{px} {py} 1.4">
            <joint armature="0" damping="0" limited="false" name="root{suffix}" pos="0 0 0" stiffness="0" type="free"/>
            <geom fromto="0 -.07 0 0 .07 0" name="torso1{suffix}" size="0.07" type="capsule"/>
            <geom name="head{suffix}" pos="0 0 .19" size=".09" type="sphere" user="258"/>
            <geom fromto="-.01 -.06 -.12 -.01 .06 -.12" name="uwaist{suffix}" size="0.06" type="capsule"/>
            <body name="lwaist{suffix}" pos="-.01 0 -0.260" quat="1.000 0 -0.002 0">
                <geom fromto="0 -.06 0 0 .06 0" name="lwaist{suffix}" size="0.06" type="capsule"/>
                <joint armature="0.02" axis="0 0 1" damping="5" name="abdomen_z{suffix}" pos="0 0 0.065" range="-45 45" stiffness="20" type="hinge"/>
                <joint armature="0.02" axis="0 1 0" damping="5" name="abdomen_y{suffix}" pos="0 0 0.065" range="-75 30" stiffness="10" type="hinge"/>
                <body name="pelvis{suffix}" pos="0 0 -0.165" quat="1.000 0 -0.002 0">
                    <joint armature="0.02" axis="1 0 0" damping="5" name="abdomen_x{suffix}" pos="0 0 0.1" range="-35 35" stiffness="10" type="hinge"/>
                    <geom fromto="-.02 -.07 0 -.02 .07 0" name="butt{suffix}" size="0.09" type="capsule"/>
                    <body name="right_thigh{suffix}" pos="0 -0.1 -0.04">
                        <joint armature="0.01" axis="1 0 0" damping="5" name="right_hip_x{suffix}" pos="0 0 0" range="-25 5" stiffness="10" type="hinge"/>
                        <joint armature="0.01" axis="0 0 1" damping="5" name="right_hip_z{suffix}" pos="0 0 0" range="-60 35" stiffness="10" type="hinge"/>
                        <joint armature="0.0080" axis="0 1 0" damping="5" name="right_hip_y{suffix}" pos="0 0 0" range="-110 20" stiffness="20" type="hinge"/>
                        <geom fromto="0 0 0 0 0.01 -.34" name="right_thigh1{suffix}" size="0.06" type="capsule"/>
                        <body name="right_shin{suffix}" pos="0 0.01 -0.403">
                            <joint armature="0.0060" axis="0 -1 0" name="right_knee{suffix}" pos="0 0 .02" range="-160 -2" type="hinge"/>
                            <geom fromto="0 0 0 0 0 -.3" name="right_shin1{suffix}" size="0.049" type="capsule"/>
                            <body name="right_foot{suffix}" pos="0 0 -0.45">
                                <geom name="right_foot{suffix}" pos="0 0 0.1" size="0.075" type="sphere" user="0"/>
                            </body>
                        </body>
                    </body>
                    <body name="left_thigh{suffix}" pos="0 0.1 -0.04">
                        <joint armature="0.01" axis="-1 0 0" damping="5" name="left_hip_x{suffix}" pos="0 0 0" range="-25 5" stiffness="10" type="hinge"/>
                        <joint armature="0.01" axis="0 0 -1" damping="5" name="left_hip_z{suffix}" pos="0 0 0" range="-60 35" stiffness="10" type="hinge"/>
                        <joint armature="0.01" axis="0 1 0" damping="5" name="left_hip_y{suffix}" pos="0 0 0" range="-110 20" stiffness="20" type="hinge"/>
                        <geom fromto="0 0 0 0 -0.01 -.34" name="left_thigh1{suffix}" size="0.06" type="capsule"/>
                        <body name="left_shin{suffix}" pos="0 -0.01 -0.403">
                            <joint armature="0.0060" axis="0 -1 0" name="left_knee{suffix}" pos="0 0 .02" range="-160 -2" stiffness="1" type="hinge"/>
                            <geom fromto="0 0 0 0 0 -.3" name="left_shin1{suffix}" size="0.049" type="capsule"/>
                            <body name="left_foot{suffix}" pos="0 0 -0.45">
                                <geom name="left_foot{suffix}" type="sphere" size="0.075" pos="0 0 0.1" user="0"/>
                            </body>
                        </body>
                    </body>
                </body>
            </body>
            <body name="right_upper_arm{suffix}" pos="0 -0.17 0.06">
                <joint armature="0.0068" axis="2 1 1" name="right_shoulder1{suffix}" pos="0 0 0" range="-85 60" stiffness="1" type="hinge"/>
                <joint armature="0.0051" axis="0 -1 1" name="right_shoulder2{suffix}" pos="0 0 0" range="-85 60" stiffness="1" type="hinge"/>
                <geom fromto="0 0 0 .16 -.16 -.16" name="right_uarm1{suffix}" size="0.04 0.16" type="capsule"/>
                <body name="right_lower_arm{suffix}" pos=".18 -.18 -.18">
                    <joint armature="0.0028" axis="0 -1 1" name="right_elbow{suffix}" pos="0 0 0" range="-90 50" stiffness="0" type="hinge"/>
                    <geom fromto="0.01 0.01 0.01 .17 .17 .17" name="right_larm{suffix}" size="0.031" type="capsule"/>
                    <geom name="right_hand{suffix}" pos=".18 .18 .18" size="0.04" type="sphere"/>
                </body>
            </body>
            <body name="left_upper_arm{suffix}" pos="0 0.17 0.06">
                <joint armature="0.0068" axis="2 -1 1" name="left_shoulder1{suffix}" pos="0 0 0" range="-60 85" stiffness="1" type="hinge"/>
                <joint armature="0.0051" axis="0 1 1" name="left_shoulder2{suffix}" pos="0 0 0" range="-60 85" stiffness="1" type="hinge"/>
                <geom fromto="0 0 0 .16 .16 -.16" name="left_uarm1{suffix}" size="0.04 0.16" type="capsule"/>
                <body name="left_lower_arm{suffix}" pos=".18 .18 -.18">
                    <joint armature="0.0028" axis="0 -1 -1" name="left_elbow{suffix}" pos="0 0 0" range="-90 50" stiffness="0" type="hinge"/>
                    <geom fromto="0.01 -0.01 0.01 .17 -.17 .17" name="left_larm{suffix}" size="0.031" type="capsule"/>
                    <geom name="left_hand{suffix}" pos=".18 -.18 .18" size="0.04" type="sphere"/>
                </body>
            </body>
        </body>"""

    def spawn_robot(self) -> dict:
        """Add a new humanoid to the scene. Rebuilds the MuJoCo model."""
        self.robot_count += 1
        new_xml = self._build_xml(self.robot_count)
        self._load_model(new_xml)
        new_robot = self.robots[-1]
        log.info("Spawned %s (total: %d)", new_robot["id"], self.robot_count)
        return {"robot_id": new_robot["id"], "total_robots": self.robot_count}

    def step(self, actions: dict[str, np.ndarray]):
        """Advance physics by one timestep.

        Args:
            actions: dict mapping robot_id -> 17-dim action array
        """
        # Apply actions to actuators
        for robot in self.robots:
            rid = robot["id"]
            if rid in actions:
                action = np.clip(actions[rid], -1.0, 1.0)
                start = robot["actuator_start"]
                self.data.ctrl[start:start + NUM_ACTUATORS] = action

        mujoco.mj_step(self.model, self.data)
        self.steps += 1

        # Check for fallen robots and reset them
        for robot in self.robots:
            qpos_start = robot["qpos_start"]
            height = self.data.qpos[qpos_start + 2]
            if height < 0.4:
                self._reset_robot(robot)

            # Battery model
            robot["battery"] = max(0.0, robot["battery"] - 0.000005)
            if robot["battery"] < 0.1:
                robot["status"] = "charging"
                robot["battery"] = min(1.0, robot["battery"] + 0.0003)
            elif robot["battery"] > 0.95:
                robot["status"] = "active"

    def _reset_robot(self, robot: dict):
        """Reset a single robot to standing pose."""
        qpos_start = robot["qpos_start"]
        qvel_start = robot["qvel_start"]

        # Standing pose: position at (x, y, 1.4), upright quaternion
        current_x = self.data.qpos[qpos_start]
        current_y = self.data.qpos[qpos_start + 1]
        self.data.qpos[qpos_start:qpos_start + BODY_QPOS_SIZE] = 0.0
        self.data.qpos[qpos_start + 0] = current_x  # keep x
        self.data.qpos[qpos_start + 1] = current_y  # keep y
        self.data.qpos[qpos_start + 2] = 1.4         # standing height
        self.data.qpos[qpos_start + 3] = 1.0         # quaternion w
        self.data.qvel[qvel_start:qvel_start + BODY_QVEL_SIZE] = 0.0

        if self.steps % 500 == 0:
            log.info("Reset %s at step %d", robot["id"], self.steps)

    def get_robot_state(self, robot_id: str) -> dict | None:
        """Extract state for a specific robot."""
        robot = self._find_robot(robot_id)
        if not robot:
            return None

        qs = robot["qpos_start"]
        vs = robot["qvel_start"]
        acts = robot["actuator_start"]

        qpos = self.data.qpos[qs:qs + BODY_QPOS_SIZE].copy()
        qvel = self.data.qvel[vs:vs + BODY_QVEL_SIZE].copy()
        forces = self.data.actuator_force[acts:acts + NUM_ACTUATORS].copy()

        pos_x, pos_y, pos_z, qx, qy, qz, qw = extract_root_pose(qpos)
        joints = extract_joint_states(qpos, qvel, forces)

        return {
            "pos_x": pos_x,
            "pos_y": pos_y,
            "pos_z": pos_z,
            "quat_x": qx,
            "quat_y": qy,
            "quat_z": qz,
            "quat_w": qw,
            "joints": joints,
            "battery": robot["battery"],
            "status": robot["status"],
            "height": pos_z,
            "steps": self.steps,
        }

    def get_all_robot_ids(self) -> list[str]:
        return [r["id"] for r in self.robots]

    def _find_robot(self, robot_id: str) -> dict | None:
        for r in self.robots:
            if r["id"] == robot_id:
                return r
        return None

    def close(self):
        pass
