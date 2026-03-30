# FleetOS

A distributed robot fleet management platform that connects simulated humanoid robots to the cloud via gRPC/Kafka, exposes developer-facing REST/WebSocket APIs with auth and billing, serves AI inference through a diffusion policy pipeline, and provides full-stack analytics via Spark + ClickHouse.

## Repository Structure

This is a monorepo containing two main components:

```
robot-fleet/
+-- platform/               # Core FleetOS platform (Go backend, Python inference, infra)
|   +-- cmd/                # Service entry points
|   +-- internal/           # Core Go packages (api, service, store, auth, etc.)
|   +-- deploy/             # Docker, Helm, Terraform configs
|   +-- analytics/          # Spark analytics pipeline
|   +-- inference/          # Python inference service (not present — see playground)
|   +-- migrations/         # PostgreSQL DDL migrations
|   +-- observability/      # Prometheus, Grafana, alerting
|   +-- sdk/                # Python + TypeScript SDKs
|   +-- training/           # Model training pipelines
|   +-- docker-compose.yml  # Local dev environment
+-- playground/             # Standalone playground app (Go + React)
|   +-- cmd/                # Playground service entry points
|   +-- internal/           # Playground Go packages
|   +-- inference/          # Python inference service
|   +-- web/                # React frontend
|   +-- deploy/             # Playground deployment configs
|   +-- docker-compose.yml  # Playground local dev
```

## Features

- **Real-time Telemetry Pipeline** -- Humanoid robots stream 20-DOF joint states, LiDAR, video, and audio over gRPC into Kafka, processed into Redis (hot state) + S3 (raw storage)
- **Developer API Platform** -- REST, WebSocket, and gRPC APIs with OpenAPI 3.1 spec, TypeScript + Python SDKs
- **Multi-tenant Auth & Billing** -- JWT + OAuth2/OIDC + API key authentication, RBAC (4 roles), per-tenant rate limiting, usage metering, 3-tier pricing (free/pro/enterprise)
- **AI Inference** -- 4-stage diffusion policy pipeline (Vision Encoder -> Language Encoder -> Cross-Attention -> Diffusion Policy), GR00T-N1 compatible
- **Durable Command Pipeline** -- Commands flow through Kafka (`robot.commands`) into Temporal workflows with full audit trail, ack tracking, and automatic retries. Supports fleet-wide dispatch.
- **Semantic Commands** -- Natural language robot control via extensible command registry (strategy pattern)
- **Model Registry** -- Model lifecycle management (staged -> canary -> deployed -> archived) with S3 artifact storage
- **FAISS Vector Search** -- Semantic search over robot fleet state ("find robots with low battery near warehouse")
- **Analytics Pipeline** -- S3 (raw NDJSON) -> Spark (batch) -> ClickHouse (OLAP) -> Redis (cache) -> API
- **ROS 2 Integration** -- Bridge node publishing to standard ROS 2 topics (JointState, PoseStamped, BatteryState, LaserScan)
- **Training Pipelines** -- Locomotion policy training and evaluation
- **Cloud-native Infrastructure** -- Kubernetes, Helm, Terraform (AWS), Docker Compose, Istio service mesh (mTLS)
- **Full Observability** -- Prometheus metrics (11 custom), Grafana dashboards (2), alerting rules (8), OpenTelemetry tracing

## Architecture

```
                    Internet
     +-----------------+------------------+
     |                 |                  |
Robot Fleet     CloudFront + WAF      NLB (TCP)
(1000s)          (HTTPS)            (gRPC/TLS)
     |                 |                  |
=====|=================|==================|===============
     |          Istio Ingress GW          |
     |          (ALB + TLS term)          |
     |                 |                  |
     |     +-----------+-----------+      |
     |     |                       |      |
     |  API Service          Ingestion Service
     |  HPA: 3-20 pods      HPA: 2-10 pods
     |  +Envoy sidecar      +Envoy sidecar
     |     |                       |
     |     v                       v
     |  Inference (GPU)      MSK (Kafka 3 brokers)
     |  Ray Serve                  |
     |  +Envoy sidecar            v
     |                       Processor -> S3/MinIO (raw NDJSON)
     |                                 -> Redis (hot state)
     |                                 -> Postgres (metadata)
     |                                        |
     |                              Spark (batch analytics)
     |                                        |
     |                              ClickHouse (OLAP)
     |                                        |
     |                              Redis (cached queries)
     |                                        |
=====|========================================|===============
     |                                        |
Observability                            CI/CD
OTel -> Prom -> Grafana -> PagerDuty     GitHub -> ECR -> ArgoCD -> EKS

AWS Managed: RDS Postgres (Multi-AZ, encrypted), ElastiCache Redis (HA),
             S3 (SSE, lifecycle), EKS (3 node groups: general/GPU/Kafka)
```

## Quick Start

### Prerequisites

- Go 1.26+
- Docker & Docker Compose
- Protocol Buffers compiler (`protoc`)

### Run Everything (Recommended)

```bash
# Start both platform and playground from repo root
./start.sh

# Verify
curl http://localhost:8080/healthz

# List robots
curl -H "X-API-Key: dev-key-001" http://localhost:8080/api/v1/robots

# Stop everything
./start.sh down
```

### Run Individually

```bash
# Platform only
./start.sh --platform

# Playground only (start platform first)
./start.sh --playground

# Or use docker compose directly
cd platform && docker compose up -d
cd playground && docker compose up -d

# Run tests
cd platform && go test -race ./internal/... ./test/...
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Health check |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/api/v1/robots` | List robots (paginated) |
| `GET` | `/api/v1/robots/{id}` | Get robot (Redis hot state -> Postgres fallback) |
| `POST` | `/api/v1/robots/{id}/command` | Send command via Kafka -> Temporal workflow |
| `POST` | `/api/v1/robots/{id}/semantic-command` | Natural language command |
| `GET` | `/api/v1/robots/{id}/telemetry` | Latest telemetry from Redis |
| `POST` | `/api/v1/inference` | AI inference (diffusion policy) |
| `GET` | `/api/v1/fleet/metrics` | Aggregated fleet stats |
| `GET` | `/api/v1/usage` | Per-tenant API usage |
| `POST` | `/api/v1/models` | Register model |
| `GET` | `/api/v1/models` | List models (filter by status) |
| `POST` | `/api/v1/models/{id}/deploy` | Deploy model |
| `GET` | `/api/v1/analytics/fleet` | Fleet hourly metrics (ClickHouse) |
| `GET` | `/api/v1/analytics/robots/{id}` | Per-robot analytics |
| `GET` | `/api/v1/analytics/anomalies` | Detected anomalies |
| `GET` | `/api/v1/ws/telemetry` | WebSocket real-time stream |

### Authentication

Three methods supported:
- **API Key**: `X-API-Key` header
- **JWT Bearer**: `Authorization: Bearer <token>` (HS256)
- **OAuth2/OIDC**: Bearer token validated against issuer's JWKS

Dev API keys: `dev-key-001` (admin), `dev-key-002` (viewer)

## Testing

```bash
cd platform

# All Go tests
go test -race ./internal/... ./test/...

# Python SDK tests
cd sdk/python && python3 -m pytest test_fleetos.py -v

# Coverage
go test -race -coverprofile=coverage.out ./...
```

## Tech Stack

| Layer | Technologies |
|-------|-------------|
| Backend | Go 1.26, gRPC, Protocol Buffers, net/http |
| Orchestration | Temporal (durable command workflows, deployment pipelines) |
| AI/ML | Python, NumPy, FAISS, SB3 PPO Policy |
| Messaging | Kafka (Confluent), gRPC bidirectional streaming |
| Storage | PostgreSQL 16, Redis 7, MinIO (S3), ClickHouse |
| Analytics | Apache Spark (PySpark), ClickHouse (OLAP) |
| API | REST, WebSocket, OpenAPI 3.1, OAuth2/OIDC, JWT |
| SDKs | TypeScript, Python (zero-dependency) |
| Robotics | ROS 2 (Humble), sensor_msgs, geometry_msgs |
| Infrastructure | Kubernetes, Docker, Helm, Terraform (AWS), Istio |
| Observability | Prometheus, Grafana, OpenTelemetry, PagerDuty |
| CI/CD | GitHub Actions, ArgoCD, GHCR |
| Security | mTLS (Istio), SSE (S3), encryption at rest (RDS/Redis), non-root containers |

## Deployment

### Docker Compose (local dev)

```bash
# Both stacks from repo root
./start.sh

# Or individually
cd platform && docker compose up -d
cd playground && docker compose up -d
```

### Kubernetes (Helm + Istio)

```bash
helm upgrade --install fleetos platform/deploy/helm/fleetos \
  --namespace fleetos --create-namespace \
  -f platform/deploy/helm/fleetos/values.yaml \
  --set istio.enabled=true
```

### Terraform (AWS)

```bash
cd platform/deploy/terraform
terraform init && terraform plan && terraform apply
```

## License

MIT
