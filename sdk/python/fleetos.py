"""
FleetOS Python SDK
Auto-generated from OpenAPI spec (docs/openapi.yaml)

Usage:
    from fleetos import FleetOS
    client = FleetOS(api_key="your-key")
    robots = client.list_robots()
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any
from urllib.request import Request, urlopen
from urllib.error import HTTPError


@dataclass
class Robot:
    id: str
    name: str
    model: str
    status: str
    pos_x: float
    pos_y: float
    pos_z: float
    battery_level: float
    last_seen: str = ""
    registered_at: str = ""
    tenant_id: str = ""
    joints: dict[str, float] = field(default_factory=dict)
    joint_velocities: dict[str, float] = field(default_factory=dict)
    joint_torques: dict[str, float] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> Robot:
        return cls(
            id=d.get("ID", d.get("robot_id", "")),
            name=d.get("Name", d.get("name", "")),
            model=d.get("Model", d.get("model", "")),
            status=d.get("Status", d.get("status", "")),
            pos_x=d.get("PosX", d.get("pos_x", 0)),
            pos_y=d.get("PosY", d.get("pos_y", 0)),
            pos_z=d.get("PosZ", d.get("pos_z", 0)),
            battery_level=d.get("BatteryLevel", d.get("battery_level", 0)),
            last_seen=str(d.get("LastSeen", d.get("last_seen", ""))),
            registered_at=str(d.get("RegisteredAt", d.get("registered_at", ""))),
            tenant_id=d.get("TenantID", d.get("tenant_id", "")),
            joints=d.get("joints", {}),
            joint_velocities=d.get("joint_velocities", {}),
            joint_torques=d.get("joint_torques", {}),
        )


@dataclass
class CommandResponse:
    command_id: int
    status: str
    robot_id: str


@dataclass
class InferenceResponse:
    predicted_actions: list[dict[str, Any]]
    confidence: float
    model_id: str
    model_version: str
    embodiment: str
    action_horizon: int
    action_dim: int
    latency_ms: int


@dataclass
class FleetMetrics:
    total_robots: int
    active_robots: int
    idle_robots: int
    error_robots: int
    avg_battery: float


class FleetOSError(Exception):
    def __init__(self, status: int, message: str):
        self.status = status
        super().__init__(f"FleetOS API error {status}: {message}")


class FleetOS:
    """FleetOS Python SDK client."""

    def __init__(self, api_key: str, base_url: str = "http://localhost:8080"):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key

    def _request(self, method: str, path: str, body: dict[str, Any] | None = None) -> Any:
        url = f"{self.base_url}{path}"
        data = json.dumps(body).encode() if body else None
        req = Request(url, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        req.add_header("X-API-Key", self.api_key)
        try:
            with urlopen(req) as resp:
                return json.loads(resp.read())
        except HTTPError as e:
            err = json.loads(e.read()) if e.fp else {}
            raise FleetOSError(e.code, err.get("error", str(e))) from None

    # --- Health ---
    def health(self) -> dict[str, str]:
        return self._request("GET", "/healthz")

    # --- Robots ---
    def list_robots(self, limit: int = 20, offset: int = 0) -> list[Robot]:
        data = self._request("GET", f"/api/v1/robots?limit={limit}&offset={offset}")
        return [Robot.from_dict(r) for r in data.get("robots", [])]

    def get_robot(self, robot_id: str) -> Robot:
        return Robot.from_dict(self._request("GET", f"/api/v1/robots/{robot_id}"))

    def get_telemetry(self, robot_id: str) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/robots/{robot_id}/telemetry")

    # --- Commands ---
    def send_command(self, robot_id: str, cmd_type: str, params: dict[str, Any] | None = None) -> CommandResponse:
        data = self._request("POST", f"/api/v1/robots/{robot_id}/command", {
            "type": cmd_type, "params": params or {},
        })
        return CommandResponse(data["command_id"], data["status"], data["robot_id"])

    def move(self, robot_id: str, x: float, y: float) -> CommandResponse:
        return self.send_command(robot_id, "move", {"x": x, "y": y})

    def stop(self, robot_id: str, emergency: bool = False) -> CommandResponse:
        return self.send_command(robot_id, "stop", {"emergency": emergency})

    def dance(self, robot_id: str) -> CommandResponse:
        return self.send_command(robot_id, "dance")

    def wave(self, robot_id: str) -> CommandResponse:
        return self.send_command(robot_id, "wave")

    def jump(self, robot_id: str) -> CommandResponse:
        return self.send_command(robot_id, "jump")

    def bow(self, robot_id: str) -> CommandResponse:
        return self.send_command(robot_id, "bow")

    # --- Semantic Commands ---
    def semantic_command(self, robot_id: str, instruction: str) -> dict[str, Any]:
        return self._request("POST", f"/api/v1/robots/{robot_id}/semantic-command", {
            "instruction": instruction, "robot_id": robot_id,
        })

    # --- Inference ---
    def run_inference(
        self,
        instruction: str,
        image: str = "",
        model_id: str = "groot-n1-v1.5",
        embodiment: str = "humanoid-v1",
    ) -> InferenceResponse:
        data = self._request("POST", "/api/v1/inference", {
            "instruction": instruction, "image": image,
            "model_id": model_id, "embodiment": embodiment,
        })
        return InferenceResponse(
            predicted_actions=data["predicted_actions"],
            confidence=data["confidence"],
            model_id=data["model_id"],
            model_version=data["model_version"],
            embodiment=data["embodiment"],
            action_horizon=data["action_horizon"],
            action_dim=data["action_dim"],
            latency_ms=data["latency_ms"],
        )

    # --- Fleet ---
    def get_fleet_metrics(self) -> FleetMetrics:
        data = self._request("GET", "/api/v1/fleet/metrics")
        return FleetMetrics(
            total_robots=data["total_robots"],
            active_robots=data["active_robots"],
            idle_robots=data["idle_robots"],
            error_robots=data["error_robots"],
            avg_battery=data["avg_battery"],
        )

    def get_usage(self) -> dict[str, Any]:
        return self._request("GET", "/api/v1/usage")

    # --- Swarm ---
    def swarm_command(self, robot_ids: list[str], cmd_type: str, params: dict[str, Any] | None = None) -> list[CommandResponse]:
        return [self.send_command(rid, cmd_type, params) for rid in robot_ids]

    def swarm_dance(self, robot_ids: list[str]) -> list[CommandResponse]:
        return self.swarm_command(robot_ids, "dance")

    def swarm_stop(self, robot_ids: list[str]) -> list[CommandResponse]:
        return self.swarm_command(robot_ids, "stop")


if __name__ == "__main__":
    # Quick demo
    client = FleetOS(api_key="dev-key-001")
    print("Health:", client.health())
    robots = client.list_robots(limit=5)
    print(f"Robots: {len(robots)}")
    for r in robots:
        print(f"  {r.id}: {r.status} bat={r.battery_level:.0%} pos=({r.pos_x:.1f}, {r.pos_y:.1f})")
    print("Metrics:", client.get_fleet_metrics())
