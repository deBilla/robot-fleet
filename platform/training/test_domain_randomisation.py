"""Domain randomisation for sim-to-real robustness (issue #17).

Per-episode randomisation of friction, link mass/inertia, action latency and
observation noise, gated behind a flag so evaluation stays deterministic.
"""

import mujoco
import numpy as np
import pytest
from stable_baselines3.common.vec_env import DummyVecEnv

import fleetos_env as fe
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
)
from observation import JOINT_POS_SLICE, NUM_ACTUATORS, TASK_SLICE

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
    inertia_before = env.model.body_inertia.copy()

    env.reset(seed=11)
    np.testing.assert_allclose(env.model.geom_friction[:, 0], friction_before)
    np.testing.assert_allclose(env.model.body_mass, mass_before)
    np.testing.assert_allclose(env.model.body_inertia, inertia_before)
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


def test_friction_uses_one_factor_per_episode():
    """Every surface shares one factor, so a foot-floor contact stays ±30%.

    MuJoCo combines two contact surfaces with the higher friction; independent
    per-surface draws would skew the contact upward.
    """
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    base = env._base_sliding_friction.copy()
    env.reset(seed=9)

    moving = base > 0
    ratios = env.model.geom_friction[:, 0][moving] / base[moving]
    np.testing.assert_allclose(ratios, ratios[0], rtol=1e-6)
    assert FRIC_MIN <= ratios[0] <= FRIC_MAX


def test_mass_and_inertia_scale_together():
    """Rotational inertia scales by the same per-body factor as mass."""
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    base_mass = env._base_mass.copy()
    base_inertia = env._base_body_inertia.copy()
    env.reset(seed=7)

    for i in env._randomisable_bodies:
        if base_mass[i] == 0:
            continue
        mass_scale = env.model.body_mass[i] / base_mass[i]
        assert MASS_MIN <= mass_scale <= MASS_MAX
        for axis in range(3):
            if base_inertia[i, axis] == 0:
                continue
            assert env.model.body_inertia[i, axis] / base_inertia[i, axis] == pytest.approx(
                mass_scale
            )


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


def test_latency_queue_applies_every_command_at_its_drawn_delay(monkeypatch):
    """Each command reaches the joints at exactly its drawn delay; none dropped.

    Commands are issued one per control step (7 substeps). The drawn latencies
    include one longer than a control tick (9), the case the old countdown
    dropped. The first command after reset is delayed too.
    """
    env = FleetOSHumanoidEnv(domain_randomisation=True, action_version=pa.ABSOLUTE)
    latencies = [5, 9, 3, 7, 4]  # substeps; distinct effect times
    draws = iter(latencies)
    monkeypatch.setattr(env, "_draw_latency_substeps", lambda: next(draws))

    observed = {}  # substep clock -> ctrl (joint targets)
    real_mj_step = fe.mujoco.mj_step

    def recording_step(model, data):
        observed[env._substep_clock] = data.ctrl[:NUM_ACTUATORS].copy()
        real_mj_step(model, data)

    monkeypatch.setattr(fe.mujoco, "mj_step", recording_step)

    env.reset(seed=0)
    dim = 11  # an actuator with a wide ctrl range
    commands = []
    for i in range(len(latencies)):
        action = np.full(NUM_ACTUATORS, -0.5 + 0.01 * i, dtype=np.float32)
        env.step(action)
        commands.append(float(np.clip(-0.5 + 0.01 * i, env._ctrl_low[dim], env._ctrl_high[dim])))

    issue_clocks = [i * N_SUBSTEPS for i in range(len(latencies))]

    # The first command must not be applied at issue time: the standing pose
    # holds until its delay elapses.
    assert observed[issue_clocks[0]][dim] != pytest.approx(commands[0])

    for i, (issue, latency) in enumerate(zip(issue_clocks, latencies)):
        apply_at = issue + latency
        assert apply_at in observed, f"command {i} never applied (scheduled at {apply_at})"
        assert observed[apply_at][dim] == pytest.approx(commands[i])
        # It must not reach the joints a substep early.
        assert observed[apply_at - 1][dim] != pytest.approx(commands[i])


def test_drawn_latency_is_in_range():
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    env.reset(seed=0)

    sub_ms = env.model.opt.timestep * 1000.0  # milliseconds per substep
    min_steps = int(round(LATENCY_MS_MIN / sub_ms))
    max_steps = int(round(LATENCY_MS_MAX / sub_ms))
    for _ in range(50):
        assert min_steps <= env._draw_latency_substeps() <= max_steps


def test_observation_noise_is_per_group_and_leaves_task_code_clean():
    env = FleetOSHumanoidEnv(domain_randomisation=True)
    env.reset(seed=0)
    obs_a = env._get_obs()
    obs_b = env._get_obs()  # same underlying MuJoCo state

    assert obs_a.dtype == np.float32
    # Measurements are perturbed...
    assert not np.allclose(obs_a[JOINT_POS_SLICE], obs_b[JOINT_POS_SLICE])
    # ...but the one-hot task code is a command, not a measurement: no noise.
    np.testing.assert_allclose(obs_a[TASK_SLICE], obs_b[TASK_SLICE])


def test_no_observation_noise_without_flag(env):
    env.reset(seed=0)
    obs_a = env._get_obs()
    obs_b = env._get_obs()  # same underlying MuJoCo state
    np.testing.assert_allclose(obs_a, obs_b)


def test_gym_make_passes_domain_randomisation_through_registration():
    """--env-id is honoured: gym.make forwards the flag to the env constructor."""
    import gymnasium as gym

    from fleetos_env import register_fleetos_env

    register_fleetos_env()
    made = gym.make("FleetOS-Humanoid-v2", domain_randomisation=True)
    try:
        assert made.unwrapped._domain_randomisation is True
    finally:
        made.close()


def test_train_make_env_uses_env_id_and_flag():
    """train_locomotion's factory builds the named env with the flag set."""
    from train_locomotion import make_env

    env = make_env("FleetOS-Humanoid-v2", domain_randomisation=True)()
    try:
        assert env.unwrapped._domain_randomisation is True
    finally:
        env.close()
