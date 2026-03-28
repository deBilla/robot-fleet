"""
FleetOS ROS 2 Bridge Node

Bridges FleetOS robot telemetry to standard ROS 2 topics, enabling
integration with RViz, ros2 topic echo, nav2, moveit2, and any
ROS 2-compatible tooling.

Data flow:
  FleetOS Redis pub/sub → This node → ROS 2 topics (JointState, PoseStamped, etc.)
  ROS 2 cmd_vel topics  → This node → FleetOS REST API → Robot commands
"""

import json
import math
import threading
from typing import Dict

import rclpy
from rclpy.node import Node
from rclpy.qos import QoSProfile, ReliabilityPolicy, DurabilityPolicy

from sensor_msgs.msg import JointState, BatteryState, LaserScan
from geometry_msgs.msg import PoseStamped, Twist, Point, Quaternion
from visualization_msgs.msg import Marker, MarkerArray
from std_msgs.msg import Header
from builtin_interfaces.msg import Time

import redis
import requests


# Standard humanoid joint names (matching FleetOS simulator)
JOINT_NAMES = [
    'head_pan', 'head_tilt',
    'left_shoulder_pitch', 'left_shoulder_roll', 'left_elbow',
    'right_shoulder_pitch', 'right_shoulder_roll', 'right_elbow',
    'left_hip_yaw', 'left_hip_roll', 'left_hip_pitch',
    'left_knee', 'left_ankle_pitch', 'left_ankle_roll',
    'right_hip_yaw', 'right_hip_roll', 'right_hip_pitch',
    'right_knee', 'right_ankle_pitch', 'right_ankle_roll',
]


def to_ros_name(robot_id: str) -> str:
    """Convert robot ID to valid ROS 2 topic name (no hyphens allowed)."""
    return robot_id.replace('-', '_')


class FleetOSBridge(Node):
    """ROS 2 node that bridges FleetOS telemetry to standard ROS topics."""

    def __init__(self):
        super().__init__('fleetos_bridge')

        # Parameters
        self.declare_parameter('redis_host', 'redis')
        self.declare_parameter('redis_port', 6379)
        self.declare_parameter('api_url', 'http://api:8080')
        self.declare_parameter('api_key', 'dev-key-001')
        self.declare_parameter('robot_count', 20)

        self.redis_host = self.get_parameter('redis_host').value
        self.redis_port = self.get_parameter('redis_port').value
        self.api_url = self.get_parameter('api_url').value
        self.api_key = self.get_parameter('api_key').value
        self.robot_count = self.get_parameter('robot_count').value

        # QoS for sensor data
        sensor_qos = QoSProfile(
            reliability=ReliabilityPolicy.BEST_EFFORT,
            durability=DurabilityPolicy.VOLATILE,
            depth=10,
        )

        # Publishers per robot (created dynamically)
        self.joint_pubs: Dict[str, any] = {}
        self.pose_pubs: Dict[str, any] = {}
        self.battery_pubs: Dict[str, any] = {}
        self.scan_pubs: Dict[str, any] = {}
        self.cmd_vel_subs: Dict[str, any] = {}

        # Pre-create publishers for expected robots
        for i in range(self.robot_count):
            robot_id = f'robot-{i:04d}'
            self._create_robot_publishers(robot_id, sensor_qos)

        # Fleet-wide marker publisher for RViz
        self.marker_pub = self.create_publisher(MarkerArray, '/fleet/markers', 10)

        # Track latest poses for marker array
        self.robot_poses: Dict[str, dict] = {}

        # Marker update timer (10 Hz)
        self.create_timer(0.1, self._publish_markers)

        # Start Redis subscriber in background thread
        self.redis_thread = threading.Thread(target=self._redis_listener, daemon=True)
        self.redis_thread.start()

        self.get_logger().info(
            f'FleetOS bridge started — redis={self.redis_host}:{self.redis_port}, '
            f'api={self.api_url}, robots={self.robot_count}'
        )

    def _create_robot_publishers(self, robot_id: str, qos: QoSProfile):
        """Create ROS 2 publishers and subscribers for a robot."""
        ros_name = to_ros_name(robot_id)
        ns = f'/fleet/{ros_name}'

        self.joint_pubs[robot_id] = self.create_publisher(
            JointState, f'{ns}/joint_states', qos)
        self.pose_pubs[robot_id] = self.create_publisher(
            PoseStamped, f'{ns}/pose', qos)
        self.battery_pubs[robot_id] = self.create_publisher(
            BatteryState, f'{ns}/battery', qos)
        self.scan_pubs[robot_id] = self.create_publisher(
            LaserScan, f'{ns}/scan', qos)

        # Subscribe to cmd_vel for this robot
        self.cmd_vel_subs[robot_id] = self.create_subscription(
            Twist, f'{ns}/cmd_vel',
            lambda msg, rid=robot_id: self._on_cmd_vel(rid, msg),
            10)

        self.get_logger().debug(f'Created publishers for {robot_id} → {ns}')

    def _redis_listener(self):
        """Background thread that subscribes to Redis telemetry and publishes ROS topics."""
        while rclpy.ok():
            try:
                r = redis.Redis(host=self.redis_host, port=self.redis_port)
                r.ping()
                self.get_logger().info('Connected to Redis')

                pubsub = r.pubsub()
                pubsub.subscribe('telemetry:all')

                for message in pubsub.listen():
                    if not rclpy.ok():
                        break
                    if message['type'] != 'message':
                        continue

                    try:
                        data = json.loads(message['data'])
                        self._publish_telemetry(data)
                    except (json.JSONDecodeError, KeyError) as e:
                        self.get_logger().debug(f'Skipping malformed message: {e}')

            except redis.ConnectionError:
                self.get_logger().warn('Redis connection lost, reconnecting in 3s...')
                import time
                time.sleep(3)
            except Exception as e:
                self.get_logger().error(f'Redis listener error: {e}')
                import time
                time.sleep(3)

    def _publish_telemetry(self, data: dict):
        """Convert FleetOS telemetry to ROS 2 messages and publish."""
        robot_id = data.get('robot_id', '')
        if not robot_id:
            return

        now = self.get_clock().now().to_msg()
        header = Header(stamp=now, frame_id='map')

        # Store for marker array
        self.robot_poses[robot_id] = data

        # --- PoseStamped ---
        if robot_id in self.pose_pubs:
            pose = PoseStamped()
            pose.header = header
            pose.pose.position = Point(
                x=float(data.get('pos_x', 0)),
                y=float(data.get('pos_y', 0)),
                z=float(data.get('pos_z', 0)),
            )
            # Convert yaw-only to quaternion (robots move in 2D plane)
            yaw = math.atan2(data.get('pos_y', 0), data.get('pos_x', 0.001))
            pose.pose.orientation = Quaternion(
                x=0.0, y=0.0,
                z=math.sin(yaw / 2),
                w=math.cos(yaw / 2),
            )
            self.pose_pubs[robot_id].publish(pose)

        # --- JointState (real data from simulator via processor) ---
        if robot_id in self.joint_pubs:
            js = JointState()
            js.header = header
            js.name = list(JOINT_NAMES)
            joint_pos = data.get('joints', {})
            joint_vel = data.get('joint_velocities', {})
            joint_torque = data.get('joint_torques', {})
            js.position = [float(joint_pos.get(n, 0)) for n in JOINT_NAMES]
            js.velocity = [float(joint_vel.get(n, 0)) for n in JOINT_NAMES]
            js.effort = [float(joint_torque.get(n, 0)) for n in JOINT_NAMES]
            self.joint_pubs[robot_id].publish(js)

        # --- BatteryState ---
        if robot_id in self.battery_pubs:
            bat = BatteryState()
            bat.header = header
            bat.percentage = float(data.get('battery_level', 0))
            bat.voltage = float(data.get('battery_voltage', 48.0 * bat.percentage))
            bat.current = float(-2.5 if data.get('status') == 'active' else 0.5)
            bat.temperature = float(data.get('motor_temp', 0))
            bat.present = True
            status_map = {
                'active': BatteryState.POWER_SUPPLY_STATUS_DISCHARGING,
                'charging': BatteryState.POWER_SUPPLY_STATUS_CHARGING,
                'idle': BatteryState.POWER_SUPPLY_STATUS_NOT_CHARGING,
            }
            bat.power_supply_status = status_map.get(
                data.get('status', ''), BatteryState.POWER_SUPPLY_STATUS_UNKNOWN)
            self.battery_pubs[robot_id].publish(bat)

    def _publish_markers(self):
        """Publish RViz markers for all robots in the fleet."""
        if not self.robot_poses:
            return

        markers = MarkerArray()
        for i, (robot_id, data) in enumerate(self.robot_poses.items()):
            marker = Marker()
            marker.header.frame_id = 'map'
            marker.header.stamp = self.get_clock().now().to_msg()
            marker.ns = 'fleet'
            marker.id = i
            marker.type = Marker.CYLINDER
            marker.action = Marker.ADD

            marker.pose.position.x = float(data.get('pos_x', 0))
            marker.pose.position.y = float(data.get('pos_y', 0))
            marker.pose.position.z = 0.5

            marker.scale.x = 0.4  # Diameter
            marker.scale.y = 0.4
            marker.scale.z = 1.0  # Height

            # Color by status
            status = data.get('status', '')
            if status == 'active':
                marker.color.r, marker.color.g, marker.color.b = 0.2, 0.9, 0.3
            elif status == 'charging':
                marker.color.r, marker.color.g, marker.color.b = 1.0, 0.7, 0.0
            elif status == 'error':
                marker.color.r, marker.color.g, marker.color.b = 1.0, 0.2, 0.2
            else:  # idle
                marker.color.r, marker.color.g, marker.color.b = 0.5, 0.5, 0.5
            marker.color.a = 0.9

            marker.lifetime.sec = 2  # Auto-expire if not updated

            # Text label
            text_marker = Marker()
            text_marker.header = marker.header
            text_marker.ns = 'fleet_labels'
            text_marker.id = i
            text_marker.type = Marker.TEXT_VIEW_FACING
            text_marker.action = Marker.ADD
            text_marker.pose.position.x = float(data.get('pos_x', 0))
            text_marker.pose.position.y = float(data.get('pos_y', 0))
            text_marker.pose.position.z = 1.2
            text_marker.scale.z = 0.3
            text_marker.color.r = 1.0
            text_marker.color.g = 1.0
            text_marker.color.b = 1.0
            text_marker.color.a = 1.0
            bat_pct = int(data.get('battery_level', 0) * 100)
            text_marker.text = f'{robot_id}\n{status} {bat_pct}%'
            text_marker.lifetime.sec = 2

            markers.markers.append(marker)
            markers.markers.append(text_marker)

        self.marker_pub.publish(markers)

    def _on_cmd_vel(self, robot_id: str, msg: Twist):
        """Handle ROS 2 cmd_vel → forward to FleetOS API as move command."""
        self.get_logger().info(
            f'cmd_vel for {robot_id}: linear=({msg.linear.x:.2f}, {msg.linear.y:.2f}), '
            f'angular={msg.angular.z:.2f}'
        )

        # Get current robot position from cache to compute target
        current = self.robot_poses.get(robot_id, {})
        cur_x = current.get('pos_x', 0.0)
        cur_y = current.get('pos_y', 0.0)

        # Simple: treat linear.x/y as delta position
        target_x = cur_x + msg.linear.x
        target_y = cur_y + msg.linear.y

        try:
            resp = requests.post(
                f'{self.api_url}/api/v1/robots/{robot_id}/command',
                headers={
                    'X-API-Key': self.api_key,
                    'Content-Type': 'application/json',
                },
                json={
                    'type': 'move',
                    'params': {'x': target_x, 'y': target_y},
                },
                timeout=2,
            )
            self.get_logger().info(f'Command sent to {robot_id}: {resp.status_code}')
        except requests.RequestException as e:
            self.get_logger().error(f'Failed to send command to {robot_id}: {e}')


def main(args=None):
    rclpy.init(args=args)
    node = FleetOSBridge()
    try:
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass
    finally:
        node.destroy_node()
        rclpy.shutdown()


if __name__ == '__main__':
    main()
