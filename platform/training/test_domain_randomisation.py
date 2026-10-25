"""Domain randomisation for sim-to-real robustness (issue #17).

Per-episode randomisation of friction, link mass, action latency and
observation noise, gated behind a flag so evaluation stays deterministic.
"""

import mujoco
import numpy as np
import pytest
from stable_baselines3.common.vec_env import DummyVecEnv

import policy_action as pa
from fleetos_env import (
    FleetOSHumanoidEnv,
    FRIC_MAX,
    FRIC_MIN,
    LATENCY_MS_MAX,
    LATENCY_MS_MIN,
    MASS_MAX,
    MASS_MIN,
    N_SUBSTEPS,
    OBS_NOISE_STD,
)
from observation import NUM_ACTUATORS

# Static scenery whose mass must not be randomised.
STATIC_SCENERY = {"world", "table", "shelf"}


def body_name(model, body_id: int) -> str:
    return mujoco.mj_id2name(model, mujoco.mjtObj.mjOBJ_BODY, body_id)


@pytest.fixture
def env():
    return FleetOSHumanoidEnv()


def test_default_env_is_deterministic_across_resets(env):
    """Without the flag, physics is identical across resets (deterministic eval)."""
    env.reset(seed=11)
    friction_before = env.model.geom_friction[:, 0].copy()
    mass_before = env.model.body_mass.copy()

    env.reset(seed=11)
    np.testing.assert_allclose(env.model.geom_friction[:, 0], friction_before)
    np.testing.assert_allclose(env.model.body_mass, mass_before)
    assert env._wind == 0.0


def test_domain_randomisation_keeps_physics_in_range():
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    base_friction = env._base_sliding_friction.copy()
    base_mass = env._base_mass.copy()

    env.reset(seed=3)
    friction = env.model.geom_friction[:, 0]
    mass = env.model.body_mass.copy()

    # Friction: every geom scaled by a factor in [FRIC_MIN, FRIC_MAX].
    assert np.all(friction >= base_friction * FRIC_MIN)
    assert np.all(friction <= base_friction * FRIC_MAX)
    # Mass: randomisable bodies scaled by a factor in [MASS_MIN, MASS_MAX].
    for i in range(env.model.nbody):
        name = body_name(env.model, i)
        if name in STATIC_SCENERY:
            assert mass[i] == base_mass[i], f"{name} is static scenery, mass must not change"
        else:
            assert mass[i] >= base_mass[i] * MASS_MIN, name
            assert mass[i] <= base_mass[i] * MASS_MAX, name


def test_domain_randomisation_differs_per_episode():
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    env.reset(seed=5)
    f1 = env.model.geom_friction[:, 0].copy()
    m1 = env.model.body_mass.copy()

    env.reset(seed=6)
    f2 = env.model.geom_friction[:, 0].copy()
    m2 = env.model.body_mass.copy()

    # Two different seeds should (overwhelmingly) produce different physics.
    assert not np.array_equal(f1, f2) or not np.array_equal(m1, m2)


def test_randomisation_layers_on_top_of_set_conditions():
    """set_conditions then reset layers domain randomisation on the preset."""
    import evaluate_policy as ep

    env = DummyVecEnv([lambda: FleetOSHumanoidEnv(domain_randomisation=True)])
    try:
        env.env_method("set_conditions", **ep.SCENARIO_PRESETS["low_friction"])
        env.envs[0].reset(seed=1)
        e = env.envs[0]
        expected_low = e._base_sliding_friction * 0.5
        fric = e.model.geom_friction[:, 0]
        assert np.all(fric >= expected_low * FRIC_MIN)
        assert np.all(fric <= expected_low * FRIC_MAX)
    finally:
        env.close()


def test_action_latency_reapplies_previous_action():
    """While latency is pending, the previously issued action is applied."""
    # ABSOLUTE: raw actions are joint targets (no to_joint_targets remap).
    env = FleetOSHumanoidEnv(domain_randomisation=True, action_version=pa.ABSOLUTE)
    env.reset(seed=0)

    old = np.full(NUM_ACTUATORS, 0.2, dtype=np.float32)
    new = np.full(NUM_ACTUATORS, -0.2, dtype=np.float32)
    old = np.clip(old, env._ctrl_low, env._ctrl_high)
    new = np.clip(new, env._ctrl_low, env._ctrl_high)

    env.step(np.zeros(NUM_ACTUATORS, dtype=np.float32))  # draw initial latency
    env._latency_substeps = N_SUBSTEPS  # force a full-step latency
    env._applied_action = old.copy()

    env.step(new)  # latency covers whole step -> `old` re-applied, `new` not yet
    np.testing.assert_allclose(env.data.ctrl[:NUM_ACTUATORS], old)

    # Once latency elapses, the new command takes effect immediately.
    env._latency_substeps = 0
    env.step(new)
    np.testing.assert_allclose(env.data.ctrl[:NUM_ACTUATORS], new)


def test_action_latency_is_drawn_in_range():
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    env.reset(seed=0)
    env.step(np.ones(NUM_ACTUATORS, dtype=np.float32))

    sub_ms = env.model.opt.timestep * 1000.0  # milliseconds per substep
    min_steps = int(round(LATENCY_MS_MIN / sub_ms))
    max_steps = int(round(LATENCY_MS_MAX / sub_ms))
    assert min_steps <= env._latency_substeps <= max_steps, env._latency_substeps


def test_observation_noise_changes_obs_with_same_state():
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    env.reset(seed=0)
    obs_a = env._get_obs()
    obs_b = env._get_obs()  # same underlying MuJoCo state
    assert obs_a.dtype == np.float32
    assert not np.allclose(obs_a, obs_b), "observation noise should perturb the obs"


def test_no_observation_noise_without_flag(env):
    env.reset(seed=0)
    obs_a = env._get_obs()
    obs_b = env._get_obs()  # same underlying MuJoCo state
    np.testing.assert_allclose(obs_a, obs_b)
