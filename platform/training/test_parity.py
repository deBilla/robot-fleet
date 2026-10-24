"""Train/serve parity: robots must see the world exactly as policies were trained.

A policy only walks for the robots if the simulator builds the same
observation, from the same scene, at the same control period as the training
and evaluation environment. These checks fail CI when the copies drift.
"""

import ast
from pathlib import Path

import pytest

TRAINING = Path(__file__).resolve().parent
REPO = TRAINING.parent.parent
SIMULATOR = REPO / "playground" / "simulator"

pytestmark = pytest.mark.skipif(not SIMULATOR.is_dir(), reason="playground/simulator not present in this checkout")


@pytest.mark.parametrize("rel", ["observation.py", "locomotion_reward.py", "policy_meta.py", "policy_action.py", "assets/lab_room.xml"])
def test_simulator_and_training_files_are_identical(rel):
    train, sim = (TRAINING / rel).read_bytes(), (SIMULATOR / rel).read_bytes()
    assert train == sim, f"{rel} differs between platform/training and playground/simulator"


def _module_constant(path: Path, name: str):
    for node in ast.parse(path.read_text()).body:
        if isinstance(node, ast.Assign) and any(getattr(t, "id", None) == name for t in node.targets):
            return ast.literal_eval(node.value)
    raise AssertionError(f"{name} not found in {path}")


def _step_substeps_default(path: Path) -> int:
    for node in ast.walk(ast.parse(path.read_text())):
        if isinstance(node, ast.FunctionDef) and node.name == "step":
            args, defaults = node.args.args, node.args.defaults
            for arg, default in zip(args[len(args) - len(defaults):], defaults):
                if arg.arg == "n_substeps":
                    return ast.literal_eval(default)
    raise AssertionError(f"step(n_substeps=...) not found in {path}")


def test_control_period_matches():
    # Both load the same lab_room.xml (asserted above), so equal substeps means
    # an equal control period (substeps x model timestep).
    train = _module_constant(TRAINING / "fleetos_env.py", "N_SUBSTEPS")
    sim = _step_substeps_default(SIMULATOR / "mujoco_env.py")
    assert train == sim, f"training acts every {train} substeps, simulator every {sim}"


def test_simulator_steps_with_default_control_period():
    # main.py must not override n_substeps, or the control period silently changes.
    src = (SIMULATOR / "main.py").read_text()
    for node in ast.walk(ast.parse(src)):
        if isinstance(node, ast.Call) and getattr(node.func, "attr", "") == "step":
            assert not any(k.arg == "n_substeps" for k in node.keywords), "main.py overrides n_substeps"


def test_both_environments_use_the_shared_reward_and_fall_rule():
    # Reward and fall termination must come from locomotion_reward.py on both
    # sides; locally re-implemented thresholds drift (sim penalised falls below
    # 0.4 while training used 0.3).
    train_src = (TRAINING / "fleetos_env.py").read_text()
    sim_src = (SIMULATOR / "mujoco_env.py").read_text()
    assert "locomotion_reward(" in train_src and "has_fallen(" in train_src
    assert "locomotion_reward(" in sim_src and "has_fallen(" in sim_src
    for name, src in (("fleetos_env.py", train_src), ("mujoco_env.py", sim_src)):
        assert "height < 0." not in src, f"{name} hard-codes a fall threshold instead of has_fallen()"


def test_inference_builds_the_same_observation_version():
    import observation
    server = REPO / "platform" / "inference" / "server.py"
    assert _module_constant(server, "OBS_VERSION") == observation.OBS_VERSION, \
        "inference serves a different observation version than training produces"


def test_robots_and_test_track_share_the_episode_cap():
    from fleetos_env import FleetOSHumanoidEnv
    from locomotion_reward import MAX_EPISODE_STEPS

    assert FleetOSHumanoidEnv()._max_episode_steps == MAX_EPISODE_STEPS
    sim_src = (REPO / "playground" / "simulator" / "mujoco_env.py").read_text()
    assert "MAX_EPISODE_STEPS" in sim_src, "robots must truncate episodes like the test track"


@pytest.mark.parametrize("name", ["ABSOLUTE", "RESIDUAL", "RESIDUAL_SCALE", "STAND_JOINT_ANGLE"])
def test_inference_maps_actions_like_the_robots(name):
    # The inference service can't import policy_action.py (separate image), so it
    # mirrors these constants; a drift would move joints differently than training.
    import policy_action
    server = REPO / "platform" / "inference" / "server.py"
    assert _module_constant(server, f"ACTION_{name}" if name in ("ABSOLUTE", "RESIDUAL") else name) \
        == getattr(policy_action, name), f"inference {name} differs from policy_action.py"
