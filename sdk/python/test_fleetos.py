"""Tests for the FleetOS Python SDK."""

import json
import unittest
from http.server import HTTPServer, BaseHTTPRequestHandler
from threading import Thread

from fleetos import FleetOS, FleetOSError, Robot, CommandResponse, FleetMetrics


class MockAPIHandler(BaseHTTPRequestHandler):
    """Mock FleetOS API server for SDK testing."""

    def do_GET(self):
        if self.path == "/healthz":
            self._json(200, {"status": "ok"})
        elif self.path.startswith("/api/v1/robots") and "?" in self.path:
            self._json(200, {
                "robots": [
                    {"ID": "robot-0001", "Name": "robot-0001", "Model": "humanoid-v1",
                     "Status": "active", "PosX": 1.0, "PosY": 2.0, "PosZ": 0.0,
                     "BatteryLevel": 0.85, "TenantID": "tenant-dev"},
                ],
                "total": 1, "limit": 20, "offset": 0,
            })
        elif self.path == "/api/v1/robots/robot-0001":
            self._json(200, {
                "ID": "robot-0001", "Name": "robot-0001", "Model": "humanoid-v1",
                "Status": "active", "PosX": 1.0, "PosY": 2.0, "PosZ": 0.0,
                "BatteryLevel": 0.85, "TenantID": "tenant-dev",
            })
        elif self.path == "/api/v1/fleet/metrics":
            self._json(200, {
                "total_robots": 10, "active_robots": 8,
                "idle_robots": 1, "error_robots": 1, "avg_battery": 0.72,
            })
        elif self.path.startswith("/api/v1/usage"):
            self._json(200, {
                "tenant_id": "tenant-dev", "date": "2026-03-28",
                "api_calls": 42, "inference_calls": 7,
            })
        elif self.path == "/api/v1/robots/nonexistent":
            self._json(404, {"error": "robot not found"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        content_len = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(content_len)) if content_len else {}

        if "/command" in self.path:
            self._json(202, {
                "command_id": 123456, "status": "queued",
                "robot_id": "robot-0001",
            })
        elif "/semantic-command" in self.path:
            self._json(202, {
                "command_id": 789, "robot_id": "robot-0001",
                "status": "queued",
                "interpreted": {"type": "wave", "params": {"instruction": body.get("instruction", "")}},
                "original": body.get("instruction", ""),
            })
        elif self.path == "/api/v1/inference":
            self._json(200, {
                "predicted_actions": [
                    {"joint": "left_shoulder_pitch", "position": 0.5, "velocity": 0.1, "torque": 2.0},
                ],
                "confidence": 0.87, "model_id": "groot-n1-v1.5",
                "model_version": "v1.5.0", "embodiment": "humanoid-v1",
                "action_horizon": 16, "action_dim": 20,
                "diffusion_steps": 10, "latency_ms": 42,
            })
        else:
            self._json(404, {"error": "not found"})

    def _json(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        pass  # Suppress request logging


class TestFleetOS(unittest.TestCase):
    """Test FleetOS SDK against mock HTTP server."""

    @classmethod
    def setUpClass(cls):
        cls.server = HTTPServer(("127.0.0.1", 0), MockAPIHandler)
        cls.port = cls.server.server_address[1]
        cls.thread = Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.client = FleetOS(api_key="dev-key-001", base_url=f"http://127.0.0.1:{cls.port}")

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def test_health(self):
        result = self.client.health()
        self.assertEqual(result["status"], "ok")

    def test_list_robots(self):
        robots = self.client.list_robots(limit=20)
        self.assertIsInstance(robots, list)
        self.assertEqual(len(robots), 1)
        self.assertIsInstance(robots[0], Robot)
        self.assertEqual(robots[0].id, "robot-0001")
        self.assertEqual(robots[0].status, "active")
        self.assertAlmostEqual(robots[0].battery_level, 0.85)

    def test_get_robot(self):
        robot = self.client.get_robot("robot-0001")
        self.assertIsInstance(robot, Robot)
        self.assertEqual(robot.id, "robot-0001")
        self.assertEqual(robot.model, "humanoid-v1")

    def test_send_command(self):
        result = self.client.send_command("robot-0001", "dance")
        self.assertIsInstance(result, CommandResponse)
        self.assertEqual(result.status, "queued")
        self.assertEqual(result.robot_id, "robot-0001")

    def test_move(self):
        result = self.client.move("robot-0001", 5.0, 3.0)
        self.assertIsInstance(result, CommandResponse)
        self.assertEqual(result.status, "queued")

    def test_dance(self):
        result = self.client.dance("robot-0001")
        self.assertEqual(result.status, "queued")

    def test_semantic_command(self):
        result = self.client.semantic_command("robot-0001", "wave hello")
        self.assertIsInstance(result, dict)
        self.assertEqual(result["interpreted"]["type"], "wave")

    def test_run_inference(self):
        result = self.client.run_inference("wave hello")
        self.assertAlmostEqual(result.confidence, 0.87)
        self.assertEqual(result.model_id, "groot-n1-v1.5")
        self.assertEqual(len(result.predicted_actions), 1)

    def test_get_fleet_metrics(self):
        metrics = self.client.get_fleet_metrics()
        self.assertIsInstance(metrics, FleetMetrics)
        self.assertEqual(metrics.total_robots, 10)
        self.assertEqual(metrics.active_robots, 8)
        self.assertAlmostEqual(metrics.avg_battery, 0.72)

    def test_get_usage(self):
        usage = self.client.get_usage()
        self.assertEqual(usage["api_calls"], 42)
        self.assertEqual(usage["inference_calls"], 7)

    def test_error_handling(self):
        with self.assertRaises(FleetOSError) as ctx:
            self.client.get_robot("nonexistent")
        self.assertEqual(ctx.exception.status, 404)

    def test_swarm_command(self):
        results = self.client.swarm_dance(["robot-0001", "robot-0001"])
        self.assertEqual(len(results), 2)
        for r in results:
            self.assertEqual(r.status, "queued")


if __name__ == "__main__":
    unittest.main()
