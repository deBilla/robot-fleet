# FleetOS

A distributed robot fleet management platform that connects simulated humanoid robots to the cloud via gRPC/Kafka, exposes developer-facing REST/WebSocket APIs with auth and billing, serves AI inference through a diffusion policy pipeline, and provides full-stack analytics via Spark + ClickHouse.

## Features

- **Real-time Telemetry Pipeline** -- Humanoid robots stream 20-DOF joint states, LiDAR, video, and audio over gRPC into Kafka, processed into Redis (hot state) + S3 (raw storage)
- **Developer API Platform** -- REST, WebSocket, and gRPC APIs with OpenAPI 3.1 spec, TypeScript + Python SDKs
- **Multi-tenant Auth & Billing** -- JWT + OAuth2/OIDC + API key authentication, RBAC (4 roles), per-tenant rate limiting, usage metering, 3-tier pricing (free/pro/enterprise)
- **AI Inference** -- 4-stage diffusion policy pipeline (Vision Encoder -> Language Encoder -> Cross-Attention -> Diffusion Policy), GR00T-N1 compatible
- **Semantic Commands** -- Natural language robot control via extensible command registry (strategy pattern)
- **Model Registry** -- Model lifecycle management (staged -> canary -> deployed -> archived) with S3 artifact storage
- **FAISS Vector Search** -- Semantic search over robot fleet state ("find robots with low battery near warehouse")
- **Analytics Pipeline** -- S3 (raw NDJSON) -> Spark (batch) -> ClickHouse (OLAP) -> Redis (cache) -> API
- **ROS 2 Integration** -- Bridge node publishing to standard ROS 2 topics (JointState, PoseStamped, BatteryState, LaserScan)
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

### Run Locally

```bash
# Start everything (Postgres, Redis, Kafka, MinIO, ClickHouse, Spark,
# Prometheus, Grafana, API, Ingestion, Processor, Simulator, Inference, Web)
docker compose up -d

# Verify
curl http://localhost:8080/healthz
# {"status":"ok"}

# List robots
curl -H "X-API-Key: dev-key-001" http://localhost:8080/api/v1/robots

# Send a command
curl -X POST -H "X-API-Key: dev-key-001" \
  -d '{"type":"dance","params":{}}' \
  http://localhost:8080/api/v1/robots/robot-0001/command

# Semantic command (natural language)
curl -X POST -H "X-API-Key: dev-key-001" \
  -d '{"instruction":"wave hello"}' \
  http://localhost:8080/api/v1/robots/robot-0001/semantic-command

# AI inference
curl -X POST -H "X-API-Key: dev-key-001" \
  -d '{"instruction":"pick up the red block"}' \
  http://localhost:8080/api/v1/inference

# Fleet analytics (from ClickHouse, cached in Redis)
curl -H "X-API-Key: dev-key-001" \
  "http://localhost:8080/api/v1/analytics/fleet?from=2026-03-28"

# Run Spark analytics job
docker exec robot-fleet-spark-master-1 /opt/spark-jobs/run.sh

# View dashboards
open http://localhost:5173   # Web playground
open http://localhost:3000   # Grafana (admin/admin)
open http://localhost:8088   # Spark UI
open http://localhost:9001   # MinIO Console (fleetos/fleetos123)
open http://localhost:8123   # ClickHouse (direct SQL)
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Health check |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/api/v1/robots` | List robots (paginated) |
| `GET` | `/api/v1/robots/{id}` | Get robot (Redis hot state -> Postgres fallback) |
| `POST` | `/api/v1/robots/{id}/command` | Send command (move, dance, wave, stop...) |
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

## Data Pipeline

```
Robots -> gRPC stream -> Ingestion -> Kafka (partitioned by robot_id)
                                         |
                                    Processor
                                    /    |    \
                              S3/MinIO  Redis  Postgres
                              (NDJSON)  (hot)  (metadata)
                                 |
                            Spark (batch)
                                 |
                            ClickHouse (OLAP)
                                 |
                            Redis (cache, 60s TTL)
                                 |
                            API endpoints
```

**Why this architecture:**
- Postgres handles metadata only (~2 writes/sec even at 1000 robots)
- S3 handles raw telemetry at any scale (partitioned by time/robot/event)
- ClickHouse handles analytical queries (pre-aggregated by Spark)
- Redis serves hot state + caches OLAP results for sub-ms API latency

## AI Inference Pipeline

The inference service implements a simplified GR00T-N1 compatible pipeline:

```
Image (224x224) + Instruction ("wave hello")
    |
Stage 1: Vision Encoder -- 196 patches (14x14) -> (196, 512) embeddings
    |
Stage 2: Language Encoder -- tokens -> (seq_len, 512) embeddings
    |
Stage 3: Cross-Attention -- fuse vision + language -> (1024,) condition vector
    |
Stage 4: Diffusion Policy -- 10 DDPM denoising steps -> (16, 20) action trajectory
    |
Output: 20 joint actions (position, velocity, torque) x 16 timesteps
```

Watch the pipeline stages: `docker logs -f robot-fleet-inference-1`

## Testing

```bash
# All Go tests (11 suites)
go test -race ./internal/... ./test/...

# Python SDK tests (12 tests)
cd sdk/python && python3 -m pytest test_fleetos.py -v

# FAISS vector search tests (14 tests)
cd inference && python3 -m pytest test_vector_search.py -v

# Coverage
go test -race -coverprofile=coverage.out ./...
```

### Test Coverage

| Package | Tests | What's tested |
|---------|-------|---------------|
| `internal/api` | 5 | JSON serialization, input validation, error paths |
| `internal/auth` | 12 | JWT, API keys, RBAC, OAuth2/OIDC validation |
| `internal/command` | 7 | All 12 command matchers, fallback, custom registry |
| `internal/config` | 5 | Env loading, defaults, invalid input warnings |
| `internal/ingestion` | 2 | Handler creation, Kafka consumer |
| `internal/middleware` | 10 | Logging, CORS, rate limiting (fail-closed), usage metering |
| `internal/service` | 17 | Robot CRUD, fleet metrics, semantic commands, billing |
| `internal/simulator` | 18 | Physics, fleet creation, protobuf, benchmarks |
| `internal/store` | 7 | Redis, Postgres, S3 key format, interface compliance |
| `test/integration` | 15 | HTTP endpoints, auth, inference mock, WebSocket, gRPC |
| `sdk/python` | 12 | All SDK methods against mock server |
| `inference` | 14 | Vector search, embeddings, FAISS index |

## Project Structure

```
robot-fleet/
+-- cmd/                        # Service entry points
|   +-- api/                    # REST API + WebSocket server
|   +-- ingestion/              # gRPC telemetry receiver
|   +-- processor/              # Kafka consumer -> S3 + Redis + Postgres
|   +-- simulator/              # Humanoid robot fleet simulator
+-- internal/                   # Core Go packages
|   +-- api/                    # Thin HTTP handlers (no business logic)
|   +-- service/                # Business logic layer (interfaces only)
|   |   +-- types.go            # DTOs + RobotService interface
|   |   +-- robot_service.go    # Robot CRUD, fleet metrics, usage
|   |   +-- command_service.go  # SendCommand, SemanticCommand
|   |   +-- inference_service.go # AI inference forwarding
|   |   +-- billing_service.go  # Pricing tiers, invoice generation
|   |   +-- model_service.go    # Model registry lifecycle
|   |   +-- analytics_service.go # ClickHouse + Redis cache
|   +-- command/                # Semantic command registry (strategy pattern)
|   +-- auth/                   # JWT, API keys, OAuth2/OIDC, RBAC
|   +-- middleware/             # Rate limiting, metrics, logging, CORS, tracing
|   +-- store/                  # PostgreSQL, Redis, S3, ClickHouse (interfaces)
|   +-- simulator/              # 20-DOF humanoid physics, fleet management
|   +-- config/                 # Environment-based configuration
+-- inference/                  # Python inference service
|   +-- server.py               # 4-stage diffusion pipeline
|   +-- vector_search.py        # FAISS semantic search
+-- analytics/                  # Spark analytics
|   +-- telemetry_analytics.py  # PySpark batch job (S3 -> ClickHouse)
|   +-- clickhouse-init.sql     # OLAP schema
|   +-- run.sh                  # Spark submit script
+-- sdk/                        # Developer SDKs
|   +-- python/                 # Python SDK + tests
|   +-- typescript/             # TypeScript SDK
+-- ros2_bridge/                # ROS 2 bridge node
+-- proto/                      # Protobuf definitions
+-- migrations/                 # PostgreSQL DDL
+-- deploy/                     # Deployment configs
|   +-- docker/                 # Dockerfiles (multi-stage, non-root)
|   +-- helm/fleetos/           # Helm chart + Istio manifests
|   +-- terraform/              # AWS (EKS, RDS, ElastiCache, S3)
+-- observability/              # Prometheus, Grafana, alerting
+-- docs/                       # OpenAPI spec, architecture diagrams
+-- test/integration/           # Integration tests
+-- docker-compose.yml          # Local dev (17 services)
```

## Tech Stack

| Layer | Technologies |
|-------|-------------|
| Backend | Go 1.26, gRPC, Protocol Buffers, net/http |
| AI/ML | Python, NumPy, FAISS, Diffusion Policy (DDPM) |
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
docker compose up -d   # 17 services
```

### Kubernetes (Helm + Istio)

```bash
helm upgrade --install fleetos deploy/helm/fleetos \
  --namespace fleetos --create-namespace \
  -f deploy/helm/fleetos/values.yaml \
  --set istio.enabled=true
```

### Terraform (AWS)

```bash
cd deploy/terraform
terraform init && terraform plan && terraform apply
```

Provisions: VPC (3 AZ), EKS (general + GPU + Kafka nodes), RDS PostgreSQL (Multi-AZ, encrypted), ElastiCache Redis (HA, encrypted), S3 (SSE, lifecycle, versioning).

## Observability

| Tool | URL | Purpose |
|------|-----|---------|
| Web Playground | http://localhost:5173 | Interactive robot dashboard |
| Grafana | http://localhost:3000 | Metrics dashboards (admin/admin) |
| Prometheus | http://localhost:9090 | Time-series metrics |
| Spark UI | http://localhost:8088 | Spark job monitoring |
| MinIO Console | http://localhost:9001 | S3 object browser (fleetos/fleetos123) |
| ClickHouse | http://localhost:8123 | Direct SQL analytics |

## License

MIT
