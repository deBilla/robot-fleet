"""
Tests for TelemetryClient — verifies error visibility, send behavior,
and persistent stream lifecycle.
Run with: python -m pytest simulator/test_telemetry_client.py
"""

import logging
import queue
import sys
import threading
import types
import unittest
from unittest.mock import MagicMock, patch, call

# ---------------------------------------------------------------------------
# Stub out grpc and generated protobuf modules so tests run without them.
# ---------------------------------------------------------------------------

grpc_mod = types.ModuleType("grpc")


class _StatusCode:
    CANCELLED = "CANCELLED"
    UNAVAILABLE = "UNAVAILABLE"


grpc_mod.StatusCode = _StatusCode


class FakeRpcError(Exception):
    def __init__(self, code):
        self._code = code

    def code(self):
        return self._code


grpc_mod.RpcError = FakeRpcError
grpc_mod.insecure_channel = MagicMock(return_value=MagicMock())
sys.modules["grpc"] = grpc_mod

# Stub protobuf modules
for mod_name in [
    "proto",
    "proto.telemetry_pb2",
    "proto.telemetry_pb2_grpc",
    "google",
    "google.protobuf",
    "google.protobuf.timestamp_pb2",
]:
    m = types.ModuleType(mod_name)
    sys.modules[mod_name] = m

# Make Timestamp a simple MagicMock
sys.modules["google.protobuf.timestamp_pb2"].Timestamp = MagicMock

# Stub all protobuf message classes used in telemetry_client
for cls in [
    "TelemetryPacket", "RobotState", "Pose", "Vector3", "Quaternion",
    "JointState", "LidarScan", "LidarPoint", "VideoFrame", "StreamAck",
]:
    setattr(sys.modules["proto.telemetry_pb2"], cls, MagicMock)

sys.modules["proto.telemetry_pb2_grpc"].TelemetryServiceStub = MagicMock

# Now import the module under test
import importlib
import os
import importlib.util

_spec = importlib.util.spec_from_file_location(
    "telemetry_client",
    os.path.join(os.path.dirname(__file__), "telemetry_client.py"),
)
_module = importlib.util.module_from_spec(_spec)
# Patch sys.path so the gen/ import inside the module works
sys.path.insert(0, os.path.dirname(__file__))
_spec.loader.exec_module(_module)
TelemetryClient = _module.TelemetryClient


def _make_client():
    """Create a TelemetryClient wired up for unit testing (no real stream thread)."""
    client = TelemetryClient("localhost:50051", "robot-0001")
    client._connected = True
    stub = MagicMock()
    client.stub = stub
    return client, stub


class TestTelemetryClientErrorVisibility(unittest.TestCase):
    """Verify that gRPC errors produce WARNING-level log entries, not DEBUG."""

    def test_stream_logs_warning_on_grpc_unavailable(self):
        """An UNAVAILABLE gRPC error in _run_stream must be logged at WARNING."""
        client, stub = _make_client()
        stub.StreamTelemetry.side_effect = FakeRpcError(_StatusCode.UNAVAILABLE)

        # Run _run_stream directly — it will hit the error, log WARNING, sleep 2s,
        # then we set stop_event so it exits on the next iteration.
        def stop_after_delay():
            import time
            time.sleep(0.1)
            client._stop_event.set()

        threading.Thread(target=stop_after_delay, daemon=True).start()

        with self.assertLogs("telemetry_client", level="WARNING") as cm:
            client._run_stream()

        self.assertTrue(
            any("WARNING" in line for line in cm.output),
            f"Expected WARNING log, got: {cm.output}",
        )

    def test_stream_does_not_raise_on_grpc_error(self):
        """gRPC errors must be caught — the stream thread must not crash."""
        client, stub = _make_client()
        stub.StreamTelemetry.side_effect = FakeRpcError(_StatusCode.UNAVAILABLE)

        # Set stop immediately so it only attempts once
        client._stop_event.set()
        # Should not raise
        client._run_stream()

    def test_cancelled_error_exits_cleanly(self):
        """CANCELLED is normal (stream closed) — must exit without warning."""
        client, stub = _make_client()
        stub.StreamTelemetry.side_effect = FakeRpcError(_StatusCode.CANCELLED)

        import io
        handler = logging.StreamHandler(io.StringIO())
        handler.setLevel(logging.WARNING)
        logger = logging.getLogger("telemetry_client")
        logger.addHandler(handler)
        try:
            client._run_stream()
            output = handler.stream.getvalue()
            self.assertEqual(output, "", f"CANCELLED should not log at WARNING, got: {output}")
        finally:
            logger.removeHandler(handler)

    def test_not_connected_returns_early(self):
        """If not connected, send_state must return immediately without queueing."""
        client, stub = _make_client()
        client._connected = False

        client.send_state({
            "pos_x": 0.0, "pos_y": 0.0, "pos_z": 0.8,
            "quat_x": 0.0, "quat_y": 0.0, "quat_z": 0.0, "quat_w": 1.0,
            "joints": [], "battery": 0.9, "status": "active",
        })

        self.assertTrue(client._packet_queue.empty())


class TestTelemetryClientSendBehavior(unittest.TestCase):
    """Verify packets are queued correctly with proper robot IDs."""

    def test_send_state_queues_packet(self):
        """send_state should put a packet on the queue."""
        client, stub = _make_client()

        client.send_state({
            "pos_x": 1.0, "pos_y": 2.0, "pos_z": 0.8,
            "quat_x": 0.0, "quat_y": 0.0, "quat_z": 0.0, "quat_w": 1.0,
            "joints": [], "battery": 0.9, "status": "active",
        })

        self.assertFalse(client._packet_queue.empty())

    def test_send_state_uses_override_robot_id(self):
        """send_state with explicit robot_id should use that, not self.robot_id."""
        client, stub = _make_client()

        # Patch RobotState to capture the robot_id it was called with
        captured = {}
        original = sys.modules["proto.telemetry_pb2"].RobotState
        mock_rs = MagicMock(side_effect=lambda **kw: (captured.update(kw), original(**kw))[1])
        sys.modules["proto.telemetry_pb2"].RobotState = mock_rs
        # Re-bind in the loaded module
        _module.RobotState = mock_rs

        try:
            client.send_state({
                "pos_x": 0.0, "pos_y": 0.0, "pos_z": 0.8,
                "quat_x": 0.0, "quat_y": 0.0, "quat_z": 0.0, "quat_w": 1.0,
                "joints": [], "battery": 0.9, "status": "active",
            }, robot_id="robot-9999")

            self.assertEqual(captured.get("robot_id"), "robot-9999")
        finally:
            sys.modules["proto.telemetry_pb2"].RobotState = original
            _module.RobotState = original

    def test_send_lidar_queues_packet(self):
        """send_lidar should put a packet on the queue."""
        client, stub = _make_client()
        client.send_lidar(robot_id="robot-0001")
        self.assertFalse(client._packet_queue.empty())

    def test_send_lidar_uses_override_robot_id(self):
        """send_lidar with explicit robot_id should use that ID in the packet."""
        client, stub = _make_client()

        # Patch TelemetryPacket to capture the robot_id
        captured = {}
        original = _module.TelemetryPacket
        mock_tp = MagicMock(side_effect=lambda **kw: (captured.update(kw), original(**kw))[1])
        _module.TelemetryPacket = mock_tp

        try:
            client.send_lidar(robot_id="robot-5555")
            self.assertEqual(captured.get("robot_id"), "robot-5555")
            self.assertFalse(client._packet_queue.empty())
        finally:
            _module.TelemetryPacket = original


class TestTelemetryClientStreamLifecycle(unittest.TestCase):
    """Verify the persistent stream reconnects and shuts down cleanly."""

    def test_close_stops_stream_thread(self):
        """close() should signal the stream thread to stop."""
        client, stub = _make_client()
        # Simulate stream returning empty acks
        stub.StreamTelemetry.return_value = iter([])

        client._stream_thread = threading.Thread(
            target=client._run_stream, daemon=True
        )
        client._stream_thread.start()

        client.close()
        self.assertTrue(client._stop_event.is_set())
        self.assertFalse(client._connected)

    def test_packet_iterator_yields_queued_packets(self):
        """_packet_iterator should yield packets from the queue."""
        client, stub = _make_client()
        client._stop_event.set()  # Will exit after draining

        mock_packet = MagicMock()
        client._packet_queue.put(mock_packet)

        packets = list(client._packet_iterator())
        self.assertEqual(len(packets), 1)
        self.assertIs(packets[0], mock_packet)


if __name__ == "__main__":
    unittest.main()
