"""
gRPC streaming client that sends TelemetryPacket messages to the ingestion service.

Uses the bidirectional StreamTelemetry RPC. Acks are consumed in a background thread.
"""

import json
import logging
import math
import os
import queue
import random
import struct
import threading
import time
from typing import Optional

import grpc
from google.protobuf.timestamp_pb2 import Timestamp

# Generated protobuf imports (generated at Docker build time into gen/)
import sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "gen"))
from proto.telemetry_pb2 import (
    TelemetryPacket, RobotState, Pose, Vector3, Quaternion,
    JointState, LidarScan, LidarPoint, VideoFrame, StreamAck,
    PerformanceMetrics,
)
from proto.telemetry_pb2_grpc import TelemetryServiceStub

log = logging.getLogger(__name__)


def _now_timestamp():
    ts = Timestamp()
    ts.GetCurrentTime()
    return ts


class TelemetryClient:
    """Streams telemetry packets to the ingestion gRPC service.

    Maintains a persistent bidirectional gRPC stream. Packets are queued
    and yielded to the stream iterator; acks are consumed in a background thread.
    """

    def __init__(self, target: str, robot_id: str):
        self.target = target
        self.robot_id = robot_id
        self.channel: Optional[grpc.Channel] = None
        self.stub: Optional[TelemetryServiceStub] = None
        self._packet_queue: queue.Queue = queue.Queue()
        self._lock = threading.Lock()
        self._connected = False
        self._stream_thread: Optional[threading.Thread] = None
        self._stop_event = threading.Event()

    def connect(self):
        """Establish gRPC channel and start the persistent bidirectional stream."""
        log.info("Connecting to ingestion at %s", self.target)
        self.channel = grpc.insecure_channel(self.target)
        self.stub = TelemetryServiceStub(self.channel)
        self._connected = True
        self._stop_event.clear()
        self._stream_thread = threading.Thread(
            target=self._run_stream, daemon=True, name="telemetry-stream"
        )
        self._stream_thread.start()
        log.info("gRPC channel ready, stream thread started")

    def send_state(self, state: dict, robot_id: Optional[str] = None):
        """Send a RobotState telemetry packet.

        Args:
            state: dict from HumanoidSim.get_state()
            robot_id: Override robot ID. Defaults to self.robot_id.
        """
        if not self._connected:
            return

        rid = robot_id or self.robot_id

        joints = []
        for name, pos, vel, torque in state["joints"]:
            joints.append(JointState(
                name=name, position=pos, velocity=vel, torque=torque
            ))

        # Build performance metrics if available
        perf_metrics = None
        if "metrics" in state:
            m = state["metrics"]
            perf_metrics = PerformanceMetrics(
                reward=m.get("reward", 0.0),
                avg_episode_reward=m.get("avg_episode_reward", 0.0),
                avg_episode_length=m.get("avg_episode_length", 0.0),
                fall_count=m.get("fall_count", 0),
                episode_count=m.get("episode_count", 0),
                uptime_pct=m.get("uptime_pct", 0.0),
                forward_velocity=m.get("forward_velocity", 0.0),
            )

        robot_state = RobotState(
            robot_id=rid,
            pose=Pose(
                position=Vector3(x=state["pos_x"], y=state["pos_y"], z=state["pos_z"]),
                orientation=Quaternion(
                    x=state["quat_x"], y=state["quat_y"],
                    z=state["quat_z"], w=state["quat_w"],
                ),
            ),
            joints=joints,
            battery_level=state["battery"],
            status=state["status"],
            timestamp=_now_timestamp(),
            metrics=perf_metrics,
        )

        packet = TelemetryPacket(
            robot_id=rid,
            timestamp=_now_timestamp(),
            state=robot_state,
        )
        self._send(packet)

    def send_lidar(self, robot_id: Optional[str] = None):
        """Send a synthetic LiDAR scan (360 points).

        Args:
            robot_id: Override robot ID. Defaults to self.robot_id.
        """
        if not self._connected:
            return

        rid = robot_id or self.robot_id
        points = []
        for i in range(360):
            angle = i * 3.14159265 * 2 / 360
            dist = 3.0 + random.random() * 2.0
            points.append(LidarPoint(
                x=dist * math.cos(angle),
                y=dist * math.sin(angle),
                z=0.5 + random.random() * 0.2,
                intensity=0.5 + random.random() * 0.5,
            ))

        scan = LidarScan(points=points, timestamp=_now_timestamp())
        packet = TelemetryPacket(
            robot_id=rid,
            timestamp=_now_timestamp(),
            lidar=scan,
        )
        self._send(packet)

    def send_video(self, robot_id: Optional[str] = None):
        """Send a synthetic video frame (random bytes as fake JPEG).

        Args:
            robot_id: Override robot ID. Defaults to self.robot_id.
        """
        if not self._connected:
            return

        rid = robot_id or self.robot_id
        fake_data = random.randbytes(50000)
        frame = VideoFrame(
            data=fake_data,
            encoding="jpeg",
            width=640,
            height=480,
            timestamp=_now_timestamp(),
        )
        packet = TelemetryPacket(
            robot_id=rid,
            timestamp=_now_timestamp(),
            video=frame,
        )
        self._send(packet)

    def _send(self, packet: TelemetryPacket):
        """Queue a packet for sending on the persistent stream."""
        if not self._connected:
            return
        self._packet_queue.put(packet)

    def _packet_iterator(self):
        """Yield packets from the queue until stop is signalled."""
        while True:
            try:
                packet = self._packet_queue.get(timeout=0.5)
                yield packet
            except queue.Empty:
                if self._stop_event.is_set():
                    return

    def _run_stream(self):
        """Background thread: maintains the persistent bidirectional stream."""
        while not self._stop_event.is_set():
            try:
                responses = self.stub.StreamTelemetry(self._packet_iterator())
                for ack in responses:
                    if not ack.success:
                        log.warning("Server nack for robot %s", ack.message_id)
                # Iterator ended normally (stop_event set)
                break
            except grpc.RpcError as e:
                if self._stop_event.is_set():
                    break
                if e.code() == grpc.StatusCode.CANCELLED:
                    break
                log.warning("gRPC stream error: %s — reconnecting in 2s", e.code())
                time.sleep(2)

    def close(self):
        self._stop_event.set()
        self._connected = False
        if self._stream_thread:
            self._stream_thread.join(timeout=5)
        if self.channel:
            self.channel.close()
