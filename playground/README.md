# FleetOS Playground

Robot simulation environment for the FleetOS platform. The playground acts like a **real robot** — it runs physics, sends telemetry (including performance metrics), and executes commands. All reasoning (inference, model management, billing) happens in the platform.

This follows the **Menlo OS architecture**: high-level reasoning in the cloud, execution on the robot.

## Architecture

```
Playground (Robot)                      Platform (Cloud Brain)
──────────────────                      ──────────────────────
Simulator (MuJoCo Humanoid-v4)
  ├── gRPC StreamTelemetry ──────────►  Ingestion :50051
  │   (20-joint state + PerformanceMetrics)  ├── Kafka (robot.telemetry)
  │   reward, uptime, falls, velocity        └── Processor → Postgres/Redis/S3
  │                                                └── Aggregates success_rate → model registry
  ├── gRPC StreamCommands  ◄──────────  Ingestion :50051
  │   (move, wave, dance, inference)        └── Kafka (robot.commands.dispatch)
  └── HTTP :8085 (/spawn, /robots)              → CommandDispatcher → gRPC
                                                → Kafka (robot.command-acks) → Temporal
Web UI :5173
  ├── Three.js 3D simulation view       API :8080
  ├── AI Inference Pipeline panel         ├── /api/v1/inference (per-robot model lookup)
  ├── Command Feed (live polling)         ├── Admin Console at /admin/
  └── Model Performance metrics           └── Temporal :7233 (workflows)

Inference :8081 (SB3 PPO, runs in platform stack)
  ├── Serves robot's assigned model (inference_model_id)
  ├── Hot-swaps from S3 on model registry deploy
  └── Polls platform API for new deployments
```

## What the Playground Does NOT Do

- No inference (runs in platform)
- No model management (platform model registry + canary deployment)
- No direct Redis or Kafka access (commands arrive via gRPC `StreamCommands`)
- No billing (platform Temporal workflows)
- No training (handled by `platform/training/`)

## Quick Start

```bash
# Platform must be running first
cd ../platform && docker compose up -d

# Start playground
docker compose up -d

# Open the web dashboard
open http://localhost:5173
```

## Services

| Service | Port | Description |
|---------|------|-------------|
| `simulator` | 8085 | MuJoCo physics (7x sub-stepping for real-time), gRPC telemetry + commands |
| `web` | 5173 | React dashboard (Three.js 3D, inference pipeline, command feed, performance metrics) |
| `ros2-bridge` | — | Bridges platform telemetry to ROS 2 topics |

## Performance Metrics

The simulator computes **Humanoid-v4 reward** every physics step and streams it back to the platform via protobuf `PerformanceMetrics`:

| Metric | Formula | Purpose |
|--------|---------|---------|
| `reward` | forward_velocity + alive_bonus(5.0) - control_cost(0.1 * ctrl^2) | Current episode accumulated reward |
| `uptime_pct` | time_standing / total_time | Becomes `success_rate` in model registry for canary evaluation |
| `fall_count` | total resets (height < 0.4m) | Stability indicator |
| `forward_velocity` | delta_x / dt | Locomotion quality |

The platform processor aggregates `uptime_pct` across all robots per model every 30s and updates the model registry's `success_rate`. This is what `CompareCanaryMetrics` uses during canary deployments — if the new model's success_rate degrades >10% vs baseline, it auto-rolls back.

## Command Flow

```
Web UI ("walk forward") → POST /api/v1/inference
  → API looks up robot's inference_model_id → sends to inference:8081
  → SB3 PPO policy predicts 17-dim action vector
  → Known command ("walk") resolved directly (smoother motion)
  → API publishes to Kafka robot.commands
  → Processor starts Temporal CommandDispatchWorkflow
  → Workflow: WriteAudit → PublishCommand → Kafka robot.commands.dispatch
  → Ingestion CommandDispatcher routes to robot's gRPC stream
  → Simulator CommandHandler receives "walk"
  → _walk_action(sim_time) produces cyclic gait torques
  → MuJoCo physics (7 sub-steps × 0.003s = real-time)
  → Robot walks, telemetry streams back with reward metrics
```

## Web Dashboard Features

- **3D Simulation View** — Three.js rendering of MuJoCo humanoid in lab room
- **AI Inference Pipeline** — 4-step visualization: instruction → inference → dispatch → robot ack (with timing)
- **Command Feed** — Live-polling command history with status badges and flash animation on new commands
- **Model Performance** — Real-time reward, avg episode reward, uptime %, velocity, falls, episode count
- **Joint Visualizer** — Per-joint angle bars with torque indicators
- **Telemetry Stream** — Raw telemetry event log

## Experience Collection for RL Training

The simulator doubles as an experience collector for offline RL. Every 5th physics step, it records a transition and periodically flushes to S3:

```
Simulator (50 Hz control loop)
  │
  Every 5th step:
  │  obs = 376-dim Humanoid-v4 observation
  │       (qpos[2:], qvel, cinert, cvel, qfrc_actuator, cfrc_ext)
  │  action = 17-dim MuJoCo control array (from command handler)
  │  reward = forward_velocity + alive_bonus(5.0) - ctrl_cost(0.1·ctrl²)
  │  done = height < 0.4m (fell)
  │
  Buffer 500 transitions → flush to S3 as NDJSON
  │
  S3: fleetos-models/experience/{date}/{robot_id}/batch_{ts}.ndjson
```

Each NDJSON line:
```json
{"robot_id":"robot-0001","obs":[1.4,0.0,...],"action":[0.1,-0.2,...],"reward":5.12,"done":false,"ts":1774915677.5}
```

The training pipeline (`platform/training/train_locomotion.py`) can consume this via `--from-experience experience/` to pre-warm reward normalization and fine-tune from real robot data.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ROBOT_ID` | robot-0001 | Initial robot identifier |
| `GRPC_TARGET` | ingestion:50051 | Ingestion gRPC endpoint |
| `SIM_STEP_HZ` | 50 | Control loop rate (physics sub-steps 7x for real-time) |
| `TELEMETRY_HZ` | 10 | Telemetry send rate |
| `CONTROL_PORT` | 8085 | HTTP control server port |
| `S3_ENDPOINT` | (empty) | MinIO/S3 endpoint for experience writing |
| `S3_BUCKET` | fleetos-models | S3 bucket for experience batches |
| `S3_ACCESS_KEY` | fleetos | S3 access key |
| `S3_SECRET_KEY` | fleetos123 | S3 secret key |
