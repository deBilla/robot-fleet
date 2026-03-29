"""
gRPC streaming client that sends TelemetryPacket messages to the ingestion service.

Uses the bidirectional StreamTelemetry RPC. Acks are consumed in a background thread.
"""

import json
import logging
import os
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
)
from proto.telemetry_pb2_grpc import TelemetryServiceStub

log = logging.getLogger(__name__)


def _now_timestamp():
    ts = Timestamp()
    ts.GetCurrentTime()
    return ts


class TelemetryClient:
    """Streams telemetry packets to the ingestion gRPC service."""

    def __init__(self, target: str, robot_id: str):
        self.target = target
        self.robot_id = robot_id
        self.channel: Optional[grpc.Channel] = None
        self.stub: Optional[TelemetryServiceStub] = None
        self._queue: list = []
        self._lock = threading.Lock()
        self._connected = False

    def connect(self):
        """Establish gRPC channel and start the bidirectional stream."""
        log.info("Connecting to ingestion at %s", self.target)
        self.channel = grpc.insecure_channel(self.target)
        self.stub = TelemetryServiceStub(self.channel)
        self._connected = True
        log.info("gRPC channel ready")

    def send_state(self, state: dict):
        """Send a RobotState telemetry packet.

        Args:
            state: dict from HumanoidSim.get_state()
        """
        if not self._connected:
            return

        joints = []
        for name, pos, vel, torque in state["joints"]:
            joints.append(JointState(
                name=name, position=pos, velocity=vel, torque=torque
            ))

        robot_state = RobotState(
            robot_id=self.robot_id,
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
        )

        packet = TelemetryPacket(
            robot_id=self.robot_id,
            timestamp=_now_timestamp(),
            state=robot_state,
        )
        self._send(packet)

    def send_lidar(self):
        """Send a synthetic LiDAR scan (360 points)."""
        if not self._connected:
            return

        points = []
        for i in range(360):
            angle = i * 3.14159265 * 2 / 360
            dist = 3.0 + random.random() * 2.0
            points.append(LidarPoint(
                x=dist * __import__("math").cos(angle),
                y=dist * __import__("math").sin(angle),
                z=0.5 + random.random() * 0.2,
                intensity=0.5 + random.random() * 0.5,
            ))

        scan = LidarScan(points=points, timestamp=_now_timestamp())
        packet = TelemetryPacket(
            robot_id=self.robot_id,
            timestamp=_now_timestamp(),
            lidar=scan,
        )
        self._send(packet)

    def send_video(self):
        """Send a synthetic video frame (random bytes as fake JPEG)."""
        if not self._connected:
            return

        fake_data = random.randbytes(50000)
        frame = VideoFrame(
            data=fake_data,
            encoding="jpeg",
            width=640,
            height=480,
            timestamp=_now_timestamp(),
        )
        packet = TelemetryPacket(
            robot_id=self.robot_id,
            timestamp=_now_timestamp(),
            video=frame,
        )
        self._send(packet)

    def _send(self, packet: TelemetryPacket):
        """Send a single packet using unary call (simpler than bidirectional stream)."""
        try:
            # Use the bidirectional stream via an iterator
            def gen():
                yield packet

            responses = self.stub.StreamTelemetry(gen())
            for ack in responses:
                pass  # consume ack
        except grpc.RpcError as e:
            if e.code() != grpc.StatusCode.CANCELLED:
                log.debug("gRPC send error: %s", e.code())

    def close(self):
        if self.channel:
            self.channel.close()
