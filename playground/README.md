# FleetOS Playground

Robot simulation environment for the FleetOS platform. Contains the physics simulator, AI inference pipeline, and ROS2 bridge.

This is a **separate Go module** (`github.com/dimuthu/robot-fleet-playground`) that connects to the FleetOS platform via gRPC and Redis.

## Architecture

```
Playground                              Platform
──────────                              ────────
Simulator (N robots)  ──gRPC telemetry──►  Ingestion :50051
  ├── 20-joint physics                     ├── Kafka producer
  ├── Gait cycles, actions                 ├── Processor → Postgres/Redis/S3
  ├── Battery/thermal sim                  └── API :8080
  └── LiDAR/video generation
                                        Platform calls Playground:
Inference :8081                         ──────────────────────────
  ├── Vision encoder (ViT)              SimulationService.ValidateAgent()
  ├── Language encoder                    (Uranus-style pre-deploy validation)
  ├── Cross-attention fusion
  └── Diffusion policy (16-step DDPM)

ROS2 Bridge
  ├── Redis → ROS2 topics
  └── /cmd_vel → FleetOS commands
```

## Quick Start

```bash
# Start the platform first
cd ../platform && docker compose up -d

# Start the playground (connects to platform network)
docker compose up -d

# Or run simulator locally
make run
```

## Shared Contracts

Both projects use the same `.proto` definitions (copied, not shared):
- `proto/telemetry.proto` — robot ↔ platform data plane
- `proto/simulation.proto` — Uranus-style validation interface

## Development

```bash
make build          # Build simulator binary
make test           # Run tests with race detector
make proto          # Regenerate protobuf Go code
make docker-build   # Build Docker images
```
