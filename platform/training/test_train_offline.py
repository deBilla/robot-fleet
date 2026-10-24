"""Tests for offline (simulator-free) training from fleet telemetry."""

import json
import random
import re
from types import SimpleNamespace
from unittest import mock

import numpy as np
import pytest

import train_offline as to

KATIB_METRIC_RE = re.compile(r"^eval_mean_reward=([+-]?\d+\.\d{2})$", re.M)


def rec(robot="r1", ep="e1", step=0, done=False, obs_val=0.0, reward=1.0,
        legacy=False, obs_len=to.OBS_DIM, act_len=to.ACT_DIM):
    r = {"robot_id": robot, "episode_id": ep, "step": step, "action": [0.1] * act_len,
         "reward": reward, "episode_done": done}
    if not legacy:
        r["obs"] = [obs_val] * obs_len
    return r


# --- build_transitions ---------------------------------------------------------

def test_links_records_in_step_order_and_handles_terminal():
    records = [rec(step=2, obs_val=2, done=True, reward=-10), rec(step=0, obs_val=0), rec(step=1, obs_val=1)]
    data, stats = to.build_transitions(records)

    assert stats["transitions"] == 3
    np.testing.assert_array_equal(data["observations"][:, 0], [0, 1, 2])
    np.testing.assert_array_equal(data["next_observations"][:, 0], [1, 2, 2])  # terminal: next_obs = obs
    np.testing.assert_array_equal(data["dones"], [0, 0, 1])
    np.testing.assert_array_equal(data["rewards"], [1, 1, -10])
    assert data["observations"].shape == (3, to.OBS_DIM) and data["actions"].shape == (3, to.ACT_DIM)
    assert data["observations"].dtype == np.float32


def test_trailing_non_terminal_record_is_dropped():
    data, stats = to.build_transitions([rec(step=0, obs_val=0), rec(step=1, obs_val=1)])
    assert stats["transitions"] == 1
    assert stats["dropped_unlinked"] == 1
    assert data["next_observations"][0, 0] == 1


def test_step_gaps_are_never_linked():
    data, stats = to.build_transitions([rec(step=0, obs_val=0), rec(step=2, obs_val=2)])
    assert stats["transitions"] == 0
    assert stats["dropped_unlinked"] == 2


def test_episodes_and_robots_are_never_linked():
    records = [
        rec(robot="r1", ep="e1", step=0, obs_val=0),
        rec(robot="r1", ep="e2", step=1, obs_val=99),  # different episode, consecutive step
        rec(robot="r2", ep="e1", step=1, obs_val=77),  # different robot, same episode id
    ]
    _, stats = to.build_transitions(records)
    assert stats["transitions"] == 0


def test_skips_legacy_and_malformed_records():
    records = [
        rec(step=0, obs_val=0), rec(step=1, obs_val=1, done=True),
        rec(step=5, legacy=True), rec(step=6, legacy=True),
        rec(step=7, obs_len=10), rec(step=8, act_len=3),
    ]
    _, stats = to.build_transitions(records)
    assert stats == {"transitions": 2, "skipped_legacy": 2, "skipped_malformed": 2, "dropped_unlinked": 0}


def test_shuffled_input_gives_same_result():
    records = [rec(robot=f"r{r}", ep=f"e{e}", step=s, obs_val=s, done=(s == 4))
               for r in range(3) for e in range(2) for s in range(5)]
    shuffled = records[:]
    random.Random(0).shuffle(shuffled)
    a, _ = to.build_transitions(records)
    b, _ = to.build_transitions(shuffled)
    assert a["observations"].shape == b["observations"].shape == (30, to.OBS_DIM)
    np.testing.assert_array_equal(np.sort(a["rewards"]), np.sort(b["rewards"]))


# --- spaces / model without a simulator -----------------------------------------------

def test_offline_spaces_match_the_policy_contract():
    env = to.OfflineSpaces()
    assert env.observation_space.shape == (to.OBS_DIM,)
    assert env.action_space.shape == (to.ACT_DIM,)


def test_offline_action_space_is_the_residual_unit_box_like_the_test_track():
    # Offline policies act like online ones: offsets around the standing pose.
    from fleetos_env import FleetOSHumanoidEnv
    track = FleetOSHumanoidEnv().action_space
    space = to.OfflineSpaces().action_space
    np.testing.assert_array_equal(space.low, track.low)
    np.testing.assert_array_equal(space.high, track.high)


def _model(tmp_path, buffer_size=8):
    args = to.build_parser().parse_args(["--job-id", "j", "--dataset-manifest", "m", "--batch-size", "4"])
    return to.make_model(args, buffer_size=buffer_size)


def test_replay_buffer_holds_fleet_joint_targets_as_residual_actions(tmp_path):
    # Fleet experience records the joint targets robots applied; the policy learns
    # the residual action that produces them.
    import policy_action as pa
    model = _model(tmp_path)
    stand, scale = pa.STAND_POSE, pa.RESIDUAL_SCALE
    targets = np.stack([stand, stand + scale, stand - scale / 2, stand + 5.0])  # last: beyond the residual range
    data = {
        "observations": np.zeros((4, to.OBS_DIM), dtype=np.float32),
        "next_observations": np.zeros((4, to.OBS_DIM), dtype=np.float32),
        "actions": targets.astype(np.float32),
        "rewards": np.zeros(4, dtype=np.float32),
        "dones": np.zeros(4, dtype=np.float32),
    }
    to.fill_replay_buffer(model, data)
    stored = model.replay_buffer.actions[:4, 0]
    np.testing.assert_allclose(stored[0], 0.0, atol=1e-5)
    np.testing.assert_allclose(stored[1], 1.0, atol=1e-5)
    np.testing.assert_allclose(stored[2], -0.5, atol=1e-5)
    np.testing.assert_allclose(stored[3], 1.0, atol=1e-5)  # clipped to the residual range


def test_trained_policy_acts_in_the_residual_unit_box(tmp_path):
    model = _model(tmp_path)
    action, _ = model.predict(np.zeros(to.OBS_DIM, dtype=np.float32), deterministic=True)
    assert np.all(np.abs(action) <= 1.0 + 1e-5)


# --- dataset loading -------------------------------------------------------------

class FakeObject:
    def __init__(self, payload: bytes):
        self._payload = payload

    def read(self):
        return self._payload

    def close(self):
        pass

    def release_conn(self):
        pass


def test_load_dataset_reads_manifest_then_listed_keys():
    manifest = {"job_id": "j", "bucket": "fleetos-telemetry", "prefix": "training_data/",
                "keys": ["training_data/a.ndjson", "training_data/b.ndjson"], "files": 2,
                "estimated_transitions": 3, "created_at": "2026-10-18T00:00:00Z"}
    objects = {
        ("fleetos-models", "datasets/j/manifest.json"): json.dumps(manifest).encode(),
        ("fleetos-telemetry", "training_data/a.ndjson"): (json.dumps(rec(step=0)) + "\n" + json.dumps(rec(step=1)) + "\n").encode(),
        ("fleetos-telemetry", "training_data/b.ndjson"): (json.dumps(rec(step=2)) + "\n\n").encode(),
    }
    client = mock.Mock()
    client.get_object.side_effect = lambda bucket, key: FakeObject(objects[(bucket, key)])
    args = SimpleNamespace(s3_bucket="fleetos-models", dataset_manifest="datasets/j/manifest.json")

    records, got_manifest = to.load_dataset(args, client=client)
    assert [r["step"] for r in records] == [0, 1, 2]
    assert got_manifest["files"] == 2


# --- run --------------------------------------------------------------------------

def make_args(tmp_path, **overrides):
    args = to.build_parser().parse_args([
        "--job-id", "job-1", "--dataset-manifest", "datasets/job-1/manifest.json",
        "--output-dir", str(tmp_path), "--gradient-steps", "3", "--batch-size", "8",
        "--min-transitions", "10", "--s3-endpoint", "minio:9000",
        "--callback-url", "http://api/cb",
    ])
    for k, v in overrides.items():
        setattr(args, k, v)
    return args


def episode_records(n):
    return [rec(step=i, obs_val=i / n, done=(i == n - 1)) for i in range(n)]


def test_parser_defaults():
    args = to.build_parser().parse_args(["--job-id", "j", "--dataset-manifest", "m"])
    assert (args.gradient_steps, args.batch_size, args.learning_rate, args.cql_alpha) == (20000, 256, 3e-4, 1.0)
    assert (args.min_transitions, args.eval_episodes, args.no_upload) == (1000, 0, False)
    assert args.env_id == "FleetOS-Humanoid-v2"


def test_too_little_data_fails_and_reports(tmp_path, capsys):
    args = make_args(tmp_path)
    with mock.patch.object(to, "load_dataset", return_value=(episode_records(5), {"files": 1})), \
         mock.patch.object(to, "notify_callback") as cb, \
         mock.patch.object(to, "upload_artifacts") as up:
        assert to.run(args) == 1

    results = json.loads((tmp_path / "results.json").read_text())
    assert results["status"] == "failed" and "transitions" in results["error_message"]
    cb.assert_called_once()
    assert cb.call_args.args[1]["status"] == "failed"
    up.assert_not_called()
    assert "offline_transitions=5" in capsys.readouterr().out


def test_trains_without_a_simulator_and_uploads(tmp_path, capsys):
    args = make_args(tmp_path)
    with mock.patch.object(to, "load_dataset", return_value=(episode_records(40), {"files": 1})), \
         mock.patch.object(to, "notify_callback") as cb, \
         mock.patch.object(to, "upload_artifacts") as up, \
         mock.patch.object(to, "evaluate_on_test_track") as ev:
        assert to.run(args) == 0

    assert (tmp_path / "policy.zip").exists()
    results = json.loads((tmp_path / "results.json").read_text())
    assert results["status"] == "completed"
    assert results["algorithm"] == "cql-sac"
    assert results["artifact_url"] == "training/job-1/policy.zip"
    assert results["dataset_manifest"] == "datasets/job-1/manifest.json"
    assert results["transitions"] == 40
    assert set(results["hyperparameters"]) == {"learning_rate", "batch_size", "cql_alpha", "gradient_steps"}
    up.assert_called_once()
    cb.assert_called_once()
    ev.assert_not_called()  # no MuJoCo unless asked for a test-track evaluation
    out = capsys.readouterr().out
    assert "offline_transitions=40" in out
    assert not KATIB_METRIC_RE.search(out)

    # The saved policy loads as SAC and acts on 86-dim observations.
    from stable_baselines3 import SAC
    model = SAC.load(str(tmp_path / "policy.zip"))
    action, _ = model.predict(np.zeros(to.OBS_DIM, dtype=np.float32), deterministic=True)
    assert action.shape == (to.ACT_DIM,)


def test_no_upload_skips_s3_writes_and_callback(tmp_path):
    args = make_args(tmp_path, no_upload=True)
    with mock.patch.object(to, "load_dataset", return_value=(episode_records(40), {"files": 1})), \
         mock.patch.object(to, "notify_callback") as cb, \
         mock.patch.object(to, "upload_artifacts") as up:
        assert to.run(args) == 0
    up.assert_not_called()
    cb.assert_not_called()


def test_test_track_eval_prints_katib_objective(tmp_path, capsys):
    args = make_args(tmp_path, eval_episodes=2, no_upload=True)
    with mock.patch.object(to, "load_dataset", return_value=(episode_records(40), {"files": 1})), \
         mock.patch.object(to, "evaluate_on_test_track", return_value=12.345) as ev:
        assert to.run(args) == 0
    ev.assert_called_once()
    assert ev.call_args.args[1:] == ("FleetOS-Humanoid-v2", 2)
    assert KATIB_METRIC_RE.search(capsys.readouterr().out).group(1) == "12.35"


def test_policy_upload_failure_fails_the_run(tmp_path):
    args = make_args(tmp_path)
    with mock.patch.object(to, "load_dataset", return_value=(episode_records(40), {"files": 1})), \
         mock.patch.object(to, "notify_callback"), \
         mock.patch.object(to, "upload_artifacts", side_effect=RuntimeError("bucket gone")):
        with pytest.raises(RuntimeError):
            to.run(args)
