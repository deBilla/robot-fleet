"""Tests for edge policy execution: robots run their assigned policy locally."""

import pickle
import threading
import types

import numpy as np
import pytest

import edge_policy as ep


class FakePolicy:
    def __init__(self, value: float):
        self.value = value
        self.calls = []

    def predict(self, obs, deterministic=False):
        self.calls.append((np.array(obs), deterministic))
        return np.full(ep.ACTION_DIM, self.value, dtype=np.float32), None


class FakePlatform:
    """Scripted policy endpoint: robot_id -> (status, body) or Exception."""

    def __init__(self):
        self.responses = {}
        self.requests = []

    def __call__(self, url, headers, timeout):
        self.requests.append((url, headers, timeout))
        rid = url.rstrip("/").split("/")[-2]
        resp = self.responses.get(rid, (404, {}))
        if isinstance(resp, Exception):
            raise resp
        return resp


def policy_body(rid, artifact, model_id="m1"):
    return (200, {"robot_id": rid, "model_id": model_id, "version": "v1",
                  "artifact_url": artifact, "source": "assigned"})


def make_manager(platform, loads):
    def fetch_and_load(artifact_url):
        loads.append(artifact_url)
        return FakePolicy(len(loads) / 10)  # stays inside the [-1, 1] action range

    return ep.EdgePolicyManager(api_url="http://api:8080", api_key="k",
                                http_get=platform, fetch_and_load=fetch_and_load,
                                refresh_s=10)


def test_action_is_none_without_policy():
    m = make_manager(FakePlatform(), [])
    m.register_robot("r1")
    assert m.action("r1", np.zeros(ep.OBS_DIM)) is None


def test_refresh_loads_assigned_policy_and_predicts_deterministically():
    platform, loads = FakePlatform(), []
    platform.responses["r1"] = policy_body("r1", "training/a/policy.zip")
    m = make_manager(platform, loads)
    m.register_robot("r1")

    m.refresh_once()
    action = m.action("r1", np.ones(ep.OBS_DIM, dtype=np.float32))

    assert loads == ["training/a/policy.zip"]
    assert action == pytest.approx(np.full(ep.ACTION_DIM, 0.1))
    url, headers, timeout = platform.requests[0]
    assert url == "http://api:8080/api/v1/robots/r1/policy"
    assert headers == {"X-API-Key": "k"}
    assert timeout and timeout > 0
    policy = m._loaded["training/a/policy.zip"][0]
    assert policy.calls[0][1] is True  # deterministic, like evaluation


def test_refresh_swaps_only_when_artifact_changes():
    platform, loads = FakePlatform(), []
    platform.responses["r1"] = policy_body("r1", "training/a/policy.zip")
    m = make_manager(platform, loads)
    m.register_robot("r1")
    m.refresh_once()
    m.refresh_once()
    assert loads == ["training/a/policy.zip"]

    platform.responses["r1"] = policy_body("r1", "training/b/policy.zip", "m2")
    m.refresh_once()
    assert loads == ["training/a/policy.zip", "training/b/policy.zip"]
    assert m.action("r1", np.zeros(ep.OBS_DIM)) == pytest.approx(np.full(ep.ACTION_DIM, 0.2))


def test_shared_artifact_is_loaded_once():
    platform, loads = FakePlatform(), []
    for rid in ("r1", "r2"):
        platform.responses[rid] = policy_body(rid, "training/a/policy.zip")
    m = make_manager(platform, loads)
    m.register_robot("r1")
    m.register_robot("r2")
    m.refresh_once()
    assert loads == ["training/a/policy.zip"]
    assert m.action("r2", np.zeros(ep.OBS_DIM)) is not None


def test_404_clears_policy_so_robot_falls_back_to_cloud():
    platform, loads = FakePlatform(), []
    platform.responses["r1"] = policy_body("r1", "training/a/policy.zip")
    m = make_manager(platform, loads)
    m.register_robot("r1")
    m.refresh_once()
    platform.responses["r1"] = (404, {})
    m.refresh_once()
    assert m.action("r1", np.zeros(ep.OBS_DIM)) is None


@pytest.mark.parametrize("failure", [ConnectionError("api down"), (500, {}), "load"])
def test_errors_keep_current_policy(failure):
    platform, loads = FakePlatform(), []
    platform.responses["r1"] = policy_body("r1", "training/a/policy.zip")
    m = make_manager(platform, loads)
    m.register_robot("r1")
    m.refresh_once()

    if failure == "load":
        platform.responses["r1"] = policy_body("r1", "training/broken/policy.zip")

        def broken(url):
            raise RuntimeError("corrupt zip")
        m._fetch_and_load = broken
    else:
        platform.responses["r1"] = failure
    m.refresh_once()

    assert m.action("r1", np.zeros(ep.OBS_DIM)) == pytest.approx(np.full(ep.ACTION_DIM, 0.1))


def test_background_refresh_thread_stops():
    m = make_manager(FakePlatform(), [])
    m.refresh_s = 0.01
    m.start()
    m.stop()
    assert not m._thread.is_alive()


def test_concurrent_action_calls_are_safe():
    platform, loads = FakePlatform(), []
    platform.responses["r1"] = policy_body("r1", "training/a/policy.zip")
    m = make_manager(platform, loads)
    m.register_robot("r1")
    m.refresh_once()
    errors = []

    def worker():
        try:
            for _ in range(200):
                m.action("r1", np.zeros(ep.OBS_DIM))
        except Exception as exc:  # pragma: no cover - surfaced below
            errors.append(exc)

    threads = [threading.Thread(target=worker) for _ in range(4)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert not errors


# ─── Observation normalization ──────────────────────────────────

def test_normalize_obs_matches_vecnormalize_formula():
    out = ep.normalize_obs(np.array([1.0, 10.0, -50.0]), np.array([0.0, 4.0, 0.0]),
                           np.array([1.0, 9.0, 1.0]), clip_obs=10.0, epsilon=1e-8)
    assert out == pytest.approx([1.0, 2.0, -10.0], rel=1e-5)
    assert out.dtype == np.float32


def test_load_obs_normalizer(tmp_path):
    stats = types.SimpleNamespace(
        obs_rms=types.SimpleNamespace(mean=np.array([1.0, 2.0]), var=np.array([4.0, 9.0])),
        clip_obs=5.0, epsilon=1e-8)
    p = tmp_path / "vec_normalize.pkl"
    p.write_bytes(pickle.dumps(stats))
    norm = ep.load_obs_normalizer(str(p))
    assert norm(np.array([3.0, 8.0])) == pytest.approx([1.0, 2.0], rel=1e-5)
    assert ep.load_obs_normalizer(str(tmp_path / "missing.pkl")) is None


def test_normalized_policy_feeds_scaled_obs():
    inner = FakePolicy(0.0)
    policy = ep.NormalizedPolicy(inner, lambda obs: np.asarray(obs) * 2)
    policy.predict(np.array([1.0, 2.0]), deterministic=True)
    assert inner.calls[0][0] == pytest.approx([2.0, 4.0])


def test_normalizer_key_is_sibling_of_artifact():
    assert ep.normalizer_key("training/j/policy.zip") == "training/j/vec_normalize.pkl"
    assert ep.normalizer_key("s3://fleetos-models/training/j/policy.zip") == "training/j/vec_normalize.pkl"
    assert ep.s3_key("s3://fleetos-models/training/j/policy.zip") == "training/j/policy.zip"


# ─── Action source selection (main loop) ────────────────────────

@pytest.mark.parametrize("task,enabled,edge_action,expected", [
    ("walk", True, np.zeros(3), "edge"),
    ("move", True, np.zeros(3), "edge"),
    ("walk", True, None, "cloud"),          # no policy loaded yet
    ("walk", False, np.zeros(3), "cloud"),  # edge disabled
    ("wave", True, np.zeros(3), "cloud"),   # gestures stay on the cloud path
    ("idle", True, np.zeros(3), "cloud"),
])
def test_choose_action_source(task, enabled, edge_action, expected):
    tasks = ep.parse_tasks("walk,move,move_relative")
    assert ep.choose_action_source(task, enabled, edge_action, tasks) == expected


def test_parse_tasks():
    assert ep.parse_tasks(" walk, move ,") == frozenset({"walk", "move"})


def test_actions_pass_through_for_physics_to_clip():
    # Policies act in each joint's control range (beyond [-1, 1] for knees and
    # hips); the simulator's physics step applies the actuator limits.
    platform = FakePlatform()
    platform.responses["r1"] = policy_body("r1", "training/a/policy.zip")
    m = ep.EdgePolicyManager(api_url="http://api", api_key="k", http_get=platform,
                             fetch_and_load=lambda url: FakePolicy(3.0))
    m.register_robot("r1")
    m.refresh_once()
    assert m.action("r1", np.zeros(ep.OBS_DIM)) == pytest.approx(np.full(ep.ACTION_DIM, 3.0))


def test_load_policy_file_real_ppo_with_vecnormalize(tmp_path):
    """Round-trip a real SB3 PPO + VecNormalize, as train_locomotion.py saves them."""
    gym = pytest.importorskip("gymnasium")
    sb3 = pytest.importorskip("stable_baselines3")
    from stable_baselines3.common.vec_env import DummyVecEnv, VecNormalize

    class Tiny(gym.Env):
        observation_space = gym.spaces.Box(-np.inf, np.inf, (ep.OBS_DIM,), np.float32)
        action_space = gym.spaces.Box(-1.0, 1.0, (ep.ACTION_DIM,), np.float32)

        def reset(self, seed=None, options=None):
            return np.zeros(ep.OBS_DIM, np.float32), {}

        def step(self, action):
            return np.ones(ep.OBS_DIM, np.float32), 0.0, False, False, {}

    venv = VecNormalize(DummyVecEnv([Tiny]))
    venv.obs_rms.mean[:] = 5.0
    model = sb3.PPO("MlpPolicy", venv, n_steps=8, batch_size=8)
    model.save(tmp_path / "policy")
    venv.save(str(tmp_path / ep.NORMALIZER_FILE))
    import policy_action
    import policy_meta
    policy_meta.write_policy_meta(tmp_path, policy_action.ABSOLUTE)  # trainers ship this next to policy.zip

    policy = ep.load_policy_file(str(tmp_path / "policy"))
    assert isinstance(policy, ep.NormalizedPolicy)
    action, _ = policy.predict(np.full(ep.OBS_DIM, 5.0, np.float32), deterministic=True)
    assert np.asarray(action).shape == (ep.ACTION_DIM,)
    # Deterministic: same input → same action.
    again, _ = policy.predict(np.full(ep.OBS_DIM, 5.0, np.float32), deterministic=True)
    assert np.allclose(action, again)


def test_normalize_obs_equals_sb3_vecnormalize():
    """Edge normalization must match what evaluation (VecNormalize) feeds the policy."""
    gym = pytest.importorskip("gymnasium")
    pytest.importorskip("stable_baselines3")
    from stable_baselines3.common.vec_env import DummyVecEnv, VecNormalize

    class Tiny(gym.Env):
        observation_space = gym.spaces.Box(-np.inf, np.inf, (ep.OBS_DIM,), np.float32)
        action_space = gym.spaces.Box(-1.0, 1.0, (ep.ACTION_DIM,), np.float32)

        def reset(self, seed=None, options=None):
            return np.zeros(ep.OBS_DIM, np.float32), {}

        def step(self, action):
            return np.zeros(ep.OBS_DIM, np.float32), 0.0, False, False, {}

    vec = VecNormalize(DummyVecEnv([Tiny]), clip_obs=3.0)
    rng = np.random.default_rng(0)
    vec.obs_rms.mean[:] = rng.normal(size=ep.OBS_DIM)
    vec.obs_rms.var[:] = rng.uniform(0.1, 4.0, size=ep.OBS_DIM)
    obs = rng.normal(scale=5.0, size=ep.OBS_DIM).astype(np.float32)

    ours = ep.normalize_obs(obs, vec.obs_rms.mean, vec.obs_rms.var, vec.clip_obs, vec.epsilon)
    assert ours == pytest.approx(vec.normalize_obs(obs), rel=1e-5, abs=1e-6)


def test_load_refuses_policy_without_matching_obs_version(tmp_path):
    import policy_meta as pm
    (tmp_path / "policy.zip").write_bytes(b"not a real zip")
    with pytest.raises(ep.IncompatiblePolicy, match="policy_meta.json"):
        ep.load_policy_file(str(tmp_path / "policy.zip"))

    (tmp_path / pm.META_FILENAME).write_text('{"obs_version": 2}')
    with pytest.raises(ep.IncompatiblePolicy, match="version 2"):
        ep.load_policy_file(str(tmp_path / "policy.zip"))


def test_residual_policy_outputs_joint_targets_around_the_standing_pose():
    import policy_action as pa
    policy = ep.JointTargetPolicy(FakePolicy(1.0), pa.RESIDUAL)
    targets, _ = policy.predict(np.zeros(ep.OBS_DIM), deterministic=True)
    assert targets == pytest.approx(pa.STAND_POSE + pa.RESIDUAL_SCALE)


def test_load_policy_file_maps_actions_by_the_policys_action_version(tmp_path, monkeypatch):
    # The manager and simulator only ever see joint targets, whatever the policy outputs.
    import policy_action as pa
    import policy_meta as pm
    monkeypatch.setattr(ep, "_load_sb3", lambda path: FakePolicy(1.0))
    (tmp_path / "policy.zip").write_bytes(b"zip")

    pm.write_policy_meta(tmp_path, pa.RESIDUAL)
    targets, _ = ep.load_policy_file(str(tmp_path / "policy.zip")).predict(np.zeros(ep.OBS_DIM))
    assert targets == pytest.approx(pa.STAND_POSE + pa.RESIDUAL_SCALE)

    pm.write_policy_meta(tmp_path, pa.ABSOLUTE)
    targets, _ = ep.load_policy_file(str(tmp_path / "policy.zip")).predict(np.zeros(ep.OBS_DIM))
    assert targets == pytest.approx(np.ones(ep.ACTION_DIM))


def test_load_refuses_unknown_action_version(tmp_path):
    from observation import OBS_VERSION
    (tmp_path / "policy.zip").write_bytes(b"zip")
    (tmp_path / "policy_meta.json").write_text(f'{{"obs_version": {OBS_VERSION}, "action_version": 99}}')
    with pytest.raises(ep.IncompatiblePolicy, match="action version 99"):
        ep.load_policy_file(str(tmp_path / "policy.zip"))
