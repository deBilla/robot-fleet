"""Policies carry the observation version they were trained on; anything
trained on a different version must be refused, never executed."""

import ast
import json
from pathlib import Path

import pytest

import policy_action as pa
import policy_meta as pm
from observation import OBS_VERSION

TRAINING = Path(__file__).resolve().parent


def test_round_trip(tmp_path):
    path = pm.write_policy_meta(tmp_path, pa.RESIDUAL)
    assert path.name == pm.META_FILENAME
    meta = pm.read_policy_meta(path)
    assert meta == {"obs_version": OBS_VERSION, "action_version": pa.RESIDUAL}
    assert pm.incompatibility(meta) is None
    assert pm.action_version(meta) == pa.RESIDUAL


def test_meta_without_action_version_means_absolute_targets():
    # Every policy trained before action versions existed commanded absolute joint targets.
    meta = {"obs_version": OBS_VERSION}
    assert pm.incompatibility(meta) is None
    assert pm.action_version(meta) == pa.ABSOLUTE


def test_unknown_action_version_is_incompatible():
    reason = pm.incompatibility({"obs_version": OBS_VERSION, "action_version": 99})
    assert reason is not None and "action version 99" in reason


def test_missing_meta_is_incompatible(tmp_path):
    meta = pm.read_policy_meta(tmp_path / pm.META_FILENAME)
    assert meta is None
    assert "policy_meta.json" in pm.incompatibility(meta)


def test_other_version_is_incompatible(tmp_path):
    (tmp_path / pm.META_FILENAME).write_text(json.dumps({"obs_version": 2}))
    reason = pm.incompatibility(pm.read_policy_meta(tmp_path / pm.META_FILENAME))
    assert reason is not None and "2" in reason and str(OBS_VERSION) in reason


def test_every_trainer_writes_policy_meta():
    for script in ["train_locomotion.py", "train_offline.py", "train_reach.py"]:
        tree = ast.parse((TRAINING / script).read_text())
        calls = {getattr(n.func, "id", getattr(n.func, "attr", None)) for n in ast.walk(tree) if isinstance(n, ast.Call)}
        assert "write_policy_meta" in calls, f"{script} uploads policies without policy_meta.json"


@pytest.mark.parametrize("script,version", [
    ("train_locomotion.py", "RESIDUAL"),
    ("train_offline.py", "RESIDUAL"),
    ("train_reach.py", "ABSOLUTE"),  # the reach env still commands absolute joint targets
])
def test_each_trainer_records_its_action_version(script, version):
    tree = ast.parse((TRAINING / script).read_text())
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and getattr(node.func, "id", None) == "write_policy_meta":
            assert any(getattr(a, "attr", getattr(a, "id", None)) == version for a in node.args), \
                f"{script} must record action version {version}"
            return
    raise AssertionError(f"{script} never calls write_policy_meta")
