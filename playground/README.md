# FleetOS Playground

Robot simulation environment for the FleetOS platform. The playground acts like a **real robot** — it only sends telemetry and executes commands. All reasoning (inference, model management) happens in the platform.

This follows the **Menlo OS architecture**: high-level reasoning in the cloud, execution on the robot.

## Architecture

```
Playground (Robot)                      Platform (Cloud Brain)
──────────────────                      ──────────────────────
Simulator (MuJoCo Humanoid-v4)
  ├── gRPC StreamTelemetry ──────────►  Ingestion :50051
  │   (20-joint state, LiDAR, video)      ├── Kafka (robot.telemetry)
  │                                       └── Processor → Postgres/Redis/S3
  ├── gRPC StreamCommands  ◄────────── Ingestion :50051
  │   (move, wave, dance, inference)      └── Bridges Redis commands → gRPC
  └── HTTP :8085 (/spawn, /robots)
                                        API :8080
Web UI :5173 ──────HTTP──────────────►   ├── /api/v1/robots, /command, /telemetry
                                         ├── /api/v1/inference → Inference :8081
                                         └── Commands → Kafka (robot.commands)
                                                          → Processor → Temporal Workflow
                                                          → Redis → gRPC → Robot

                                        Temporal :7233 (workflow orchestration)
                                         ├── CommandDispatchWorkflow (audit + ack tracking)
                                         ├── Optional inference step (RunInference activity)
                                         └── UI at :8233

                                        Inference :8081 (runs in platform stack)
                                         ├── SB3 PPO policy serving
                                         ├── Loads model from MinIO (S3)
                                         └── Polls model registry for new deploys
```

## What the Playground Does NOT Do

- No inference (runs in platform)
- No model management (platform model registry)
- No direct Redis or Kafka access (commands arrive via gRPC `StreamCommands`)
- No Temporal interaction (workflows are platform-side only)
- No training (handled by `platform/training/`)

## Quick Start

```bash
# Start both platform + playground from repo root
../start.sh

# Or start individually (platform must be running first)
cd ../platform && docker compose up -d
docker compose up -d

# Open the web dashboard
open http://localhost:5173
```

## Services

| Service | Port | Description |
|---------|------|-------------|
| `simulator` | 8085 | MuJoCo Humanoid-v4 physics, gRPC telemetry/commands |
| `web` | 5173 | React dashboard (Three.js 3D view, inference panel) |
| `ros2-bridge` | — | Bridges platform telemetry to ROS 2 topics |

## Communication

The simulator communicates with the platform exclusively via gRPC:

- **StreamTelemetry** (bidirectional): Sends `TelemetryPacket` (robot state, LiDAR, video), receives `StreamAck`
- **StreamCommands** (server-stream): Sends `CommandRequest(robot_id)`, receives `RobotCommand` stream with `command_type` field (move, wave, dance, etc.)

Both streams auto-reconnect on error.

## Command Flow

When a user sends a command or runs inference from the Web UI:

```
Web UI ("walk") → POST /api/v1/inference → Platform API
    → Inference server (PPO model) → predicted actions
    → API publishes CommandMessage to Kafka (robot.commands)
    → Processor starts Temporal workflow (visible at :8233)
    → Workflow: WriteAudit → [optional RunInference] → PublishCommand (Redis)
    → Ingestion bridges Redis → gRPC StreamCommands
    → Simulator CommandHandler receives "walk"
    → get_action(step) returns cyclic walking gait torques
    → MuJoCo applies torques → robot walks
```

For known locomotion commands (walk, wave, dance, etc.), the robot runs its own built-in continuous action loops for smoother motion. For unknown instructions, the model's raw predicted torques are applied directly.

## Shared Contracts

Both projects use the same `.proto` definitions (copied, not shared):
- `proto/telemetry.proto` — robot ↔ platform data plane (TelemetryPacket, RobotCommand, StreamCommands)
- `proto/simulation.proto` — Uranus-style validation interface (ValidateAgent, RunScenario)

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ROBOT_ID` | robot-0001 | Initial robot identifier |
| `GRPC_TARGET` | ingestion:50051 | Ingestion gRPC endpoint |
| `SIM_STEP_HZ` | 50 | Physics step rate |
| `TELEMETRY_HZ` | 10 | Telemetry send rate |
| `CONTROL_PORT` | 8085 | HTTP control server port |
