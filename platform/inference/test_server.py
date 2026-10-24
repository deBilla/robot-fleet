"""Tests for inference server: joint mapping, per-artifact policy cache, fallback."""

import json
import sys
import threading
import types
from unittest import mock

import numpy as np
import pytest

import server


class FakePolicy:
    def __init__(self, value: float):
        self.value = value

    def predict(self, obs, deterministic=False):
        return np.full(server.MUJOCO_ACTION_DIM, self.value, dtype=np.float32), None


# ─── Joint mapping ──────────────────────────────────────────────

def test_action_mapping_covers_every_actuator():
    assert sorted(server.MUJOCO_TO_FLEET) == list(range(server.MUJOCO_ACTION_DIM))
    # Mapping is one-to-one onto the fleet joint schema.
    assert sorted(server.MUJOCO_TO_FLEET.values()) == list(range(server.FLEET_ACTION_DIM))


def test_mujoco_to_fleet_actions_returns_all_25_joints():
    actions = np.arange(server.MUJOCO_ACTION_DIM, dtype=np.float32) / 100.0
    out = server.mujoco_to_fleet_actions(actions)
    assert len(out) == 25
    by_name = {a["joint"]: a for a in out}
    assert "torso_roll" in by_name
    # abdomen_x (actuator 2) is no longer dropped
    assert by_name["torso_roll"]["torque"] == pytest.approx(0.02)


# ─── Policy cache ───────────────────────────────────────────────

def test_policy_cache_evicts_least_recently_used():
    loads = []

    def fetch(url):
        loads.append(url)
        return FakePolicy(len(loads))

    cache = server.PolicyCache(2, fetch)
    cache.get("a")
    cache.get("b")
    cache.get("a")          # hit; "a" becomes most recent
    cache.get("c")          # evicts "b"
    assert cache.keys() == ["a", "c"]
    cache.get("b")          # reload
    assert loads == ["a", "b", "c", "b"]


def test_policy_cache_returns_per_entry_lock():
    cache = server.PolicyCache(2, lambda url: FakePolicy(1.0))
    policy, lock = cache.get("a")
    assert isinstance(policy, FakePolicy)
    assert isinstance(lock, type(threading.Lock()))
    assert cache.get("a")[1] is lock


def test_policy_cache_does_not_cache_failures():
    calls = {"n": 0}

    def fetch(url):
        calls["n"] += 1
        raise RuntimeError("s3 down")

    cache = server.PolicyCache(2, fetch)
    for _ in range(2):
        with pytest.raises(RuntimeError):
            cache.get("a")
    assert calls["n"] == 2
    assert cache.keys() == []


# ─── Policy selection / inference ───────────────────────────────

@pytest.fixture
def active_model(monkeypatch):
    active = server.ActiveModel(model_id="active-model", version="v-active",
                                artifact_url="training/active/policy.zip")
    monkeypatch.setattr(server, "_active_model", active)
    monkeypatch.setattr(server, "_policy", FakePolicy(0.0))
    return active


def test_select_policy_without_artifact_uses_active(active_model):
    policy, lock, model_id, version, fallback = server.select_policy()
    assert policy is server._policy
    assert lock is server._policy_lock
    assert (model_id, version, fallback) == ("active-model", "v-active", False)


def test_select_policy_with_active_artifact_uses_active(active_model):
    _, _, model_id, _, fallback = server.select_policy("training/active/policy.zip", "x", "y")
    assert model_id == "active-model"
    assert fallback is False


def test_run_inference_uses_artifact_specific_policy(active_model, monkeypatch):
    canary = FakePolicy(0.3)
    cache = server.PolicyCache(4, mock.Mock(return_value=canary))
    monkeypatch.setattr(server, "_policy_cache", cache)

    result = server.run_inference(None, "stand still", "canary-model", "humanoid-v4",
                                  artifact_url="training/canary/policy.zip",
                                  model_version="v-canary")

    assert result["model_id"] == "canary-model"
    assert result["model_version"] == "v-canary"
    assert result["fallback"] is False
    assert len(result["predicted_actions"]) == 25
    assert cache.keys() == ["training/canary/policy.zip"]


def test_run_inference_falls_back_when_artifact_fails(active_model, monkeypatch):
    cache = server.PolicyCache(4, mock.Mock(side_effect=RuntimeError("missing")))
    monkeypatch.setattr(server, "_policy_cache", cache)

    result = server.run_inference(None, "stand still", "canary-model", "humanoid-v4",
                                  artifact_url="training/missing/policy.zip",
                                  model_version="v-canary")

    assert result["fallback"] is True
    assert result["model_id"] == "active-model"
    assert result["model_version"] == "v-active"


def test_run_inference_falls_back_when_canary_predict_fails(active_model, monkeypatch):
    broken = mock.Mock()
    broken.predict.side_effect = IndexError("wrong obs shape")
    monkeypatch.setattr(server, "_policy_cache", server.PolicyCache(4, lambda url: broken))

    result = server.run_inference(None, "reach", "sac-model", "humanoid-v4",
                                  artifact_url="training/sac/policy.zip")

    assert result["fallback"] is True
    assert result["model_id"] == "active-model"


# ─── Observation shaping ────────────────────────────────────────

class GoalSpace:
    spaces = {"observation": None, "achieved_goal": None, "desired_goal": None}


def test_build_policy_obs_flat_for_regular_policy():
    obs = np.zeros(server.OBS_DIM, dtype=np.float32)
    assert server.build_policy_obs(FakePolicy(0), obs, None) is obs


def test_build_policy_obs_dict_for_goal_conditioned_policy():
    policy = mock.Mock(observation_space=GoalSpace())
    obs = np.arange(server.OBS_DIM, dtype=np.float32)
    out = server.build_policy_obs(policy, obs, [1.0, 2.0, 3.0])
    assert out["observation"] is obs
    np.testing.assert_array_equal(out["achieved_goal"], [60.0, 61.0, 62.0])  # right hand
    np.testing.assert_array_equal(out["desired_goal"], [1.0, 2.0, 3.0])


def test_build_policy_obs_without_goal_targets_current_hand():
    policy = mock.Mock(observation_space=GoalSpace())
    obs = np.arange(server.OBS_DIM, dtype=np.float32)
    out = server.build_policy_obs(policy, obs, None)
    np.testing.assert_array_equal(out["desired_goal"], out["achieved_goal"])


# ─── Startup model lookup ───────────────────────────────────────

def _fake_models_response(models):
    resp = mock.MagicMock()
    resp.read.return_value = __import__("json").dumps({"models": models}).encode()
    resp.__enter__.return_value = resp
    return resp


def test_poll_platform_model_sends_api_key(active_model, monkeypatch):
    monkeypatch.setenv("PLATFORM_API_KEY", "dev-key-002")
    models = [{"id": "m1", "version": "v1", "artifact_url": "training/m1/policy.zip"}]
    with mock.patch("urllib.request.urlopen", return_value=_fake_models_response(models)) as urlopen:
        assert server._poll_platform_model("http://api:8080") == "training/m1/policy.zip"
    req = urlopen.call_args[0][0]
    assert req.get_header("X-api-key") == "dev-key-002"
    assert server._active_model.snapshot() == ("m1", "v1")


def test_poll_platform_model_without_key_sends_no_header(active_model, monkeypatch):
    monkeypatch.delenv("PLATFORM_API_KEY", raising=False)
    with mock.patch("urllib.request.urlopen", return_value=_fake_models_response([])) as urlopen:
        assert server._poll_platform_model("http://api:8080") == ""
    assert urlopen.call_args[0][0].get_header("X-api-key") is None


# ─── Loading ────────────────────────────────────────────────────

# A stub stable_baselines3 keeps torch out of the test process: importing torch
# alongside faiss (test_vector_search.py) aborts on macOS (duplicate OpenMP).
@pytest.fixture
def fake_sb3(monkeypatch):
    sb3 = types.ModuleType("stable_baselines3")
    sb3.PPO = mock.Mock()
    sb3.SAC = mock.Mock()
    buffers = types.ModuleType("stable_baselines3.common.buffers")
    buffers.DictReplayBuffer = object()
    monkeypatch.setitem(sys.modules, "stable_baselines3", sb3)
    monkeypatch.setitem(sys.modules, "stable_baselines3.common", types.ModuleType("stable_baselines3.common"))
    monkeypatch.setitem(sys.modules, "stable_baselines3.common.buffers", buffers)
    return sb3


@pytest.fixture
def compatible_dir(tmp_path):
    """A model directory shipped the way trainers ship it (with policy_meta.json)."""
    (tmp_path / server.META_FILENAME).write_text(json.dumps({"obs_version": server.OBS_VERSION}))
    return tmp_path


def test_load_policy_file_prefers_ppo(fake_sb3, compatible_dir):
    ppo_model = object()
    fake_sb3.PPO.load.return_value = ppo_model
    assert server.load_policy_file(str(compatible_dir / "policy")) is ppo_model
    fake_sb3.SAC.load.assert_not_called()


def test_load_policy_file_falls_back_to_sac(fake_sb3, compatible_dir):
    sac_model = object()
    fake_sb3.PPO.load.side_effect = ValueError("not ppo")
    fake_sb3.SAC.load.return_value = sac_model
    assert server.load_policy_file(str(compatible_dir / "policy")) is sac_model
    # HER policies need the replay buffer swapped to load without an env.
    custom = fake_sb3.SAC.load.call_args.kwargs["custom_objects"]
    assert custom["replay_buffer_class"] is sys.modules["stable_baselines3.common.buffers"].DictReplayBuffer


def test_load_policy_file_raises_when_neither_loads(fake_sb3, compatible_dir):
    fake_sb3.PPO.load.side_effect = ValueError("a")
    fake_sb3.SAC.load.side_effect = ValueError("b")
    with pytest.raises(RuntimeError):
        server.load_policy_file(str(compatible_dir / "policy"))


def test_artifact_cache_path_is_per_artifact_and_not_model_path():
    a = server._artifact_cache_path("training/a/policy.zip")
    b = server._artifact_cache_path("training/b/policy.zip")
    assert a != b
    assert a != str(server.MODEL_PATH)


# ─── Observation normalization (VecNormalize stats) ────────────

def test_normalize_obs_matches_vecnormalize_formula():
    obs = np.array([1.0, 10.0, -50.0], dtype=np.float32)
    mean = np.array([0.0, 4.0, 0.0])
    var = np.array([1.0, 9.0, 1.0])
    out = server.normalize_obs(obs, mean, var, clip_obs=10.0, epsilon=1e-8)
    assert out[0] == pytest.approx(1.0, rel=1e-5)
    assert out[1] == pytest.approx(2.0, rel=1e-5)
    assert out[2] == pytest.approx(-10.0)  # clipped
    assert out.dtype == np.float32


def _write_vecnormalize(path, mean, var, clip_obs=10.0, epsilon=1e-8):
    import pickle
    stats = types.SimpleNamespace(
        obs_rms=types.SimpleNamespace(mean=np.asarray(mean, dtype=np.float64), var=np.asarray(var, dtype=np.float64)),
        clip_obs=clip_obs, epsilon=epsilon)
    with open(path, "wb") as f:
        pickle.dump(stats, f)


def test_load_obs_normalizer_reads_pickle(tmp_path):
    p = tmp_path / "vec_normalize.pkl"
    _write_vecnormalize(p, [1.0, 2.0], [4.0, 9.0], clip_obs=5.0)
    norm = server.load_obs_normalizer(str(p))
    out = norm(np.array([3.0, 8.0], dtype=np.float32))
    assert out == pytest.approx([1.0, 2.0], rel=1e-5)


def test_load_obs_normalizer_missing_file_is_none(tmp_path):
    assert server.load_obs_normalizer(str(tmp_path / "nope.pkl")) is None


def test_load_policy_file_wraps_with_sibling_normalizer(fake_sb3, compatible_dir):
    tmp_path = compatible_dir
    seen = {}

    class Recording:
        observation_space = None

        def predict(self, obs, deterministic=False):
            seen["obs"] = np.array(obs)
            return np.zeros(server.MUJOCO_ACTION_DIM, dtype=np.float32), None

    fake_sb3.PPO.load.return_value = Recording()
    _write_vecnormalize(tmp_path / "vec_normalize.pkl", [1.0, 1.0], [4.0, 4.0])

    policy = server.load_policy_file(str(tmp_path / "policy"))
    policy.predict(np.array([3.0, 5.0], dtype=np.float32), deterministic=True)
    assert seen["obs"] == pytest.approx([1.0, 2.0], rel=1e-5)


def test_load_policy_file_without_normalizer_is_unwrapped(fake_sb3, compatible_dir):
    tmp_path = compatible_dir
    model = object()
    fake_sb3.PPO.load.return_value = model
    assert server.load_policy_file(str(tmp_path / "policy")) is model


def test_normalizer_path_is_sibling_of_policy():
    assert server.normalizer_path("/m/cache/abc/policy") == "/m/cache/abc/vec_normalize.pkl"
    assert server.normalizer_path("/m/humanoid-v4-ppo.zip") == "/m/vec_normalize.pkl"
    assert server.normalizer_key("training/job-1/policy.zip") == "training/job-1/vec_normalize.pkl"
    assert server.normalizer_key("s3://b/training/job-1/policy.zip") == "training/job-1/vec_normalize.pkl"


# ─── Instruction bias only for the stub ─────────────────────────

def test_learned_policy_actions_are_not_biased(active_model):
    result = server.run_inference(None, "wave hello", "", "humanoid-v4")
    assert result["bias_applied"] is False
    assert result["action"] == pytest.approx([0.0] * server.MUJOCO_ACTION_DIM)


def test_learned_policy_actions_keep_their_joint_range(active_model, monkeypatch):
    # Policies act in each actuator's ctrlrange (knee to -2.8 rad); robots clip
    # to that range in physics, so serving must not cut targets to [-1, 1].
    monkeypatch.setattr(server, "_policy", FakePolicy(-2.5))
    result = server.run_inference(None, "walk", "", "humanoid-v4")
    assert result["action"] == pytest.approx([-2.5] * server.MUJOCO_ACTION_DIM)
    assert {a["torque"] for a in result["predicted_actions"]} == {-2.5}


def test_stub_policy_actions_are_biased(active_model, monkeypatch):
    monkeypatch.setattr(server, "_policy", server._StubPolicy())
    result = server.run_inference(None, "wave hello", "", "humanoid-v4")
    assert result["bias_applied"] is True
    assert any(abs(a) > 0 for a in result["action"])


def test_inference_predicts_deterministically_like_evaluation(active_model, monkeypatch):
    calls = []

    class Recording(FakePolicy):
        def predict(self, obs, deterministic=False):
            calls.append(deterministic)
            return super().predict(obs, deterministic)

    monkeypatch.setattr(server, "_policy", Recording(0.1))
    server.run_inference(None, "walk", "", "humanoid-v4")
    assert calls == [True]


# ─── Observation-version compatibility ──────────────────────────

def test_load_policy_file_refuses_policy_without_meta(fake_sb3, tmp_path):
    with pytest.raises(server.IncompatiblePolicy, match="policy_meta.json"):
        server.load_policy_file(str(tmp_path / "policy"))
    fake_sb3.PPO.load.assert_not_called()


def test_load_policy_file_refuses_other_obs_version(fake_sb3, tmp_path):
    (tmp_path / server.META_FILENAME).write_text('{"obs_version": 2}')
    with pytest.raises(server.IncompatiblePolicy, match="version 2"):
        server.load_policy_file(str(tmp_path / "policy"))


def test_load_model_serves_stub_for_incompatible_policy(fake_sb3, tmp_path, monkeypatch):
    monkeypatch.setattr(server, "MODEL_PATH", tmp_path / "humanoid-v4-ppo")
    (tmp_path / "humanoid-v4-ppo.zip").write_bytes(b"zip")  # cached model, no policy_meta.json
    assert isinstance(server.load_model(), server._StubPolicy)



# ─── Action versions ────────────────────────────────────────────

def _meta(dir_, action_version=None):
    meta = {"obs_version": server.OBS_VERSION}
    if action_version is not None:
        meta["action_version"] = action_version
    (dir_ / server.META_FILENAME).write_text(json.dumps(meta))


def test_residual_policy_is_served_as_joint_targets(fake_sb3, tmp_path):
    fake_sb3.PPO.load.return_value = FakePolicy(1.0)
    _meta(tmp_path, server.ACTION_RESIDUAL)
    policy = server.load_policy_file(str(tmp_path / "policy"))
    targets, _ = policy.predict(np.zeros(server.OBS_DIM), deterministic=True)
    expected = server.STAND_JOINT_ANGLE + server.RESIDUAL_SCALE
    assert np.asarray(targets) == pytest.approx(np.full(server.MUJOCO_ACTION_DIM, expected))


def test_policy_without_action_version_is_served_unchanged(fake_sb3, tmp_path):
    model = FakePolicy(1.0)
    fake_sb3.PPO.load.return_value = model
    _meta(tmp_path)  # predates action versions: absolute joint targets
    assert server.load_policy_file(str(tmp_path / "policy")) is model


def test_unknown_action_version_is_refused(fake_sb3, tmp_path):
    _meta(tmp_path, 99)
    with pytest.raises(server.IncompatiblePolicy, match="action version 99"):
        server.load_policy_file(str(tmp_path / "policy"))
