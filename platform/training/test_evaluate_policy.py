"""Tests for evaluate_policy.py's model loading (online PPO and offline SAC models)."""

from unittest import mock

import pytest

import evaluate_policy as ep


def test_loads_ppo_models():
    sentinel = object()
    with mock.patch.object(ep.PPO, "load", return_value=sentinel) as ppo, \
         mock.patch.object(ep.SAC, "load") as sac:
        model, algo = ep.load_policy("policy.zip")
    assert model is sentinel and algo == "PPO"
    ppo.assert_called_once_with("policy.zip")
    sac.assert_not_called()


def test_falls_back_to_sac_for_offline_models():
    sentinel = object()
    with mock.patch.object(ep.PPO, "load", side_effect=ValueError("not a PPO zip")), \
         mock.patch.object(ep.SAC, "load", return_value=sentinel):
        model, algo = ep.load_policy("policy.zip")
    assert model is sentinel and algo == "SAC"


def test_unloadable_model_raises_with_all_reasons():
    with mock.patch.object(ep.PPO, "load", side_effect=ValueError("ppo says no")), \
         mock.patch.object(ep.SAC, "load", side_effect=ValueError("sac says no")):
        with pytest.raises(RuntimeError, match="ppo says no.*sac says no"):
            ep.load_policy("policy.zip")


def test_real_offline_policy_round_trip(tmp_path):
    """A model saved by train_offline loads and acts through the evaluator's loader."""
    import numpy as np

    import train_offline as to

    args = to.build_parser().parse_args(["--job-id", "j", "--dataset-manifest", "m", "--batch-size", "4"])
    model = to.make_model(args, buffer_size=8)
    model.save(str(tmp_path / "policy"))

    loaded, algo = ep.load_policy(str(tmp_path / "policy.zip"))
    assert algo == "SAC"
    action, _ = loaded.predict(np.zeros(to.OBS_DIM, dtype=np.float32), deterministic=True)
    assert action.shape == (to.ACT_DIM,)


def test_refuses_policies_without_matching_obs_version(tmp_path):
    import policy_meta as pm
    model = tmp_path / "policy.zip"
    model.write_bytes(b"zip")
    with pytest.raises(ep.IncompatiblePolicy, match="policy_meta.json"):
        ep.check_compatible(str(model))
    pm.write_policy_meta(tmp_path, 2)
    ep.check_compatible(str(model))  # compatible: no exception


@pytest.mark.parametrize("version", [1, 2])
def test_test_track_runs_a_policy_in_its_own_action_version(tmp_path, version):
    import policy_meta as pm
    model = tmp_path / "policy.zip"
    model.write_bytes(b"zip")
    pm.write_policy_meta(tmp_path, version)
    env = ep.make_test_track(str(model), "FleetOS-Humanoid-v2")
    assert env.envs[0].unwrapped.action_version == version
