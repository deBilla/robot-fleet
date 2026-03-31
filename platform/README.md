# FleetOS

A distributed robot fleet management platform following the **Menlo OS architecture**: high-level reasoning happens in the platform (cloud); robots handle execution only. Robots connect via gRPC to send telemetry and receive commands. The platform runs inference, manages model lifecycle (staged → canary → deployed), and provides developer-facing REST/WebSocket APIs with auth, billing, and full-stack analytics.

## Features

- **Real-time Telemetry Pipeline** -- Humanoid robots stream 20-DOF joint states, LiDAR, video, and audio over gRPC into Kafka, processed into Redis (hot state) + S3 (raw storage)
- **Developer API Platform** -- REST, WebSocket, and gRPC APIs with OpenAPI 3.1 spec, TypeScript + Python SDKs
- **Multi-tenant Auth & Billing** -- JWT + OAuth2/OIDC + API key authentication, RBAC (4 roles), per-tenant rate limiting, usage metering, 3-tier pricing (free/pro/enterprise)
- **AI Inference** -- SB3 PPO policy serving with instruction bias, loads models from S3/MinIO, hot-swaps on deployment
- **Durable Command Pipeline** -- Commands published to Kafka (`robot.commands`), consumed by processor, orchestrated via Temporal workflows with audit trail, ack tracking, retries, and timeout handling
- **Semantic Commands** -- Natural language robot control via extensible command registry (strategy pattern), with FAISS-backed resolution for unmatched instructions (searches fleet state to resolve spatial commands like "go where the idle robots are")
- **Model Registry** -- Model lifecycle management (staged -> canary -> deployed -> archived) with S3 artifact storage, canary deployment via Temporal workflows
- **Admin Console** -- React SPA at `/admin/` for tenant management, DB-backed API key lifecycle (create/revoke with SHA-256 hashing), billing overview
- **Temporal Billing** -- Per-tenant BillingCycleWorkflow: 6h usage aggregation (Redis → Postgres), monthly invoice generation, payment processing with dunning retries, tier change signals
- **Canary Model Deployment** -- Progressive rollout (5% → 25% → 50% → 100%) with deterministic robot selection (fnv32 hash), live success_rate comparison from robot uptime metrics, automatic rollback
- **Per-Robot Model Assignment** -- Each robot tracks its `inference_model_id`; inference lookups use the assigned model; canary deployments progressively update assignments
- **Performance Metrics Feedback** -- Simulator computes Humanoid-v4 reward (forward velocity + alive bonus - control cost), tracks falls/uptime, streams metrics via protobuf → processor aggregates into model registry `success_rate`
- **Kafka Event Pipeline** -- Commands, acks, and deployment events all flow through Kafka topics (no Redis pub/sub for events); Redis is cache + rate limiter only
- **Temporal Workflow Orchestration** -- Command dispatch, model deployment, agent deployment, billing cycles, and webhook delivery all run as durable Temporal workflows with full visibility via Temporal UI
- **FAISS Vector Search** -- Semantic search over robot fleet state via `/resolve` endpoint, indexes robots from Redis hot state every 5s, resolves natural language instructions to concrete commands with spatial context
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
# From repo root — start both platform and playground
../start.sh

# Or start platform only
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
| `GET` | `/admin/` | Admin console (React SPA) |
| `GET` | `/api/v1/robots` | List robots (paginated, includes model assignment) |
| `GET` | `/api/v1/robots/{id}` | Get robot (hot state + inference_model_id) |
| `POST` | `/api/v1/robots/{id}/command` | Send command → Kafka → Temporal workflow |
| `GET` | `/api/v1/robots/{id}/commands` | Command history with audit trail |
| `POST` | `/api/v1/inference` | AI inference (uses robot's assigned model) |
| `GET` | `/api/v1/fleet/metrics` | Aggregated fleet stats |
| `POST` | `/api/v1/models` | Register model (staged) |
| `POST` | `/api/v1/models/{id}/deploy` | Deploy model (canary rollout) |
| `GET` | `/api/v1/billing/subscription` | Current subscription |
| `PUT` | `/api/v1/billing/subscription/tier` | Change tier (signals workflow) |
| `GET` | `/api/v1/billing/invoices` | Invoice history |
| `POST` | `/api/v1/admin/tenants` | Create tenant + API key (admin only) |
| `GET` | `/api/v1/admin/tenants` | List tenants (admin only) |
| `POST` | `/api/v1/admin/tenants/{id}/keys` | Generate API key (admin only) |
| `DELETE` | `/api/v1/admin/keys/{hash}` | Revoke API key (admin only) |
| `GET` | `/api/v1/analytics/fleet` | Fleet hourly metrics (ClickHouse) |
| `GET` | `/api/v1/ws/telemetry` | WebSocket real-time stream |

### Authentication

Three methods supported:
- **API Key**: `X-API-Key` header (DB-backed with SHA-256 hashing, or hardcoded dev keys)
- **JWT Bearer**: `Authorization: Bearer <token>` (HS256)
- **OAuth2/OIDC**: Bearer token validated against issuer's JWKS

Dev keys: `dev-key-001` (admin/enterprise), `dev-key-002` (viewer/free)

RBAC: `admin`, `operator`, `developer`, `viewer`. Admin routes under `/api/v1/admin/` require `RequireRole(admin)`.

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

## Command Pipeline (Kafka + Temporal)

```
REST API /command ──┐
                    ├──> Kafka (robot.commands) ──> Processor ──> Temporal Workflow
REST API /inference ┘    (keyed by robot_id)                           |
                                                         ┌─────────────┼─────────────┐
                                                         v             v             v
                                                    WriteAudit    [RunInference]  PublishCommand
                                                    (Postgres)     (optional)     (Kafka dispatch topic)
                                                                                      |
                                                                      CommandDispatcher (1 consumer, N robots)
                                                                                      |
                                                                          gRPC StreamCommands -> Robot
                                                                                      |
                                                                      Kafka robot.command-acks
                                                                                      |
                                                                      AckBridge (Kafka consumer) -> Signal Workflow
```

**Kafka topics:**
- `robot.commands` — API → processor (triggers Temporal workflow)
- `robot.commands.dispatch` — Temporal activity → ingestion (commands to robots)
- `robot.command-acks` — ingestion → worker (robot acknowledgments back to Temporal)
- `deployment.events` — Temporal activities → SSE consumers (agent deployment progress)

All event routing goes through Kafka. Redis is used only for cache (hot state, rate limiting, usage counters, dedup).

**Workflow lifecycle:** requested → dispatched → acked/timeout

## AI Inference Pipeline

The inference service runs as a platform service (not on the robot), following the Menlo OS pattern where robots only execute — all reasoning is cloud-side.

```
Instruction ("wave hello") + Optional observation (376-dim MuJoCo state)
    |
SB3 PPO Policy -- predict() → 17-dim MuJoCo action vector
    |
Instruction Bias -- overlay command-specific joint targets (wave, dance, walk...)
    |
MuJoCo-to-Fleet Mapping -- 17 actuators → 20 joint schema
    |
Output: 20 joint actions (position, velocity, torque)
    |
API publishes CommandMessage to Kafka (robot.commands)
    |
Processor starts Temporal workflow → Redis → Ingestion → gRPC StreamCommands
```

**Model lifecycle**: Training (`platform/training/`) → S3 upload → Model Registry (staged → canary → deployed) → Inference hot-swaps model from S3.

Watch inference: `docker logs -f platform-inference-1`

## ML Training Pipeline (Temporal + Kubeflow)

Training is orchestrated by Temporal and executed by Kubeflow on dedicated GPU nodes.

```
Temporal TrainingPipelineWorkflow (fleetos-training queue)
  │
  ├─ CollectExperienceStats
  │     Query S3 for robot experience batches
  │     (obs, action, reward, done) NDJSON from simulator
  │
  ├─ SubmitKubeflowRun ──────────► Kubeflow (kubeflow namespace)
  │     │                            ├─ Katib HPO (Bayesian, 20 trials, 3 parallel)
  │     │                            │    lr ∈ [0.0001, 0.001]
  │     │                            │    batch ∈ {32, 64, 128}
  │     │                            │    epochs ∈ [5, 15]
  │     │                            ├─ PyTorchJob on g5.2xlarge GPU nodes
  │     │                            │    FleetOS-Humanoid-v1 custom env
  │     │                            │    7x sub-stepping, lab_room.xml physics
  │     │                            └─ Artifacts → S3 (policy.zip, metrics.json)
  │     └─ Waits for completion
  │
  ├─ EvaluateTrainedModel
  │     Compare vs baseline: must be ≥ 95% of current success_rate
  │     Gate: reject if degraded
  │
  ├─ RegisterTrainedModel → model registry (staged)
  │
  └─ Auto-deploy (if enabled)
        └─ Child ModelDeploymentWorkflow (canary 5% → 25% → 50% → 100%)
```

**Separation of concerns:**
- **Temporal** = orchestration brain (decides what, when, handles failures, gates, rollback)
- **Kubeflow** = ML compute engine (GPU training, hyperparameter tuning, distributed jobs)

### Experience Collection

The simulator collects RL experience at runtime and writes to S3:

```
Simulator (50 Hz physics loop)
  → Every 5th step: record (obs, action, reward, done)
  → obs: 376-dim Humanoid-v4 observation (qpos, qvel, cinert, cvel, forces)
  → Buffer 500 transitions → flush to S3 as NDJSON
  → S3 key: experience/{date}/{robot_id}/batch_{timestamp}.ndjson
```

Training jobs can consume this experience for:
- Pre-warming reward normalization (VecNormalize running stats)
- Fine-tuning from deployed policy + real data
- Future: offline RL (CQL, IQL, Decision Transformer)

### Custom Training Environment

`training/fleetos_env.py` registers `FleetOS-Humanoid-v1` — a Gymnasium env using `lab_room.xml`:
- Same physics as production simulator (0.003s timestep × 7 sub-steps)
- Same reward function (forward velocity + alive bonus - control cost)
- Same floor friction, walls, obstacles
- Ensures trained policy works in the environment it's deployed to

### Training Modes

```bash
# From scratch on custom lab env (default)
python train_locomotion.py --job-id job-001 --timesteps 2000000

# Fine-tune from deployed model with experience data
python train_locomotion.py --job-id job-002 --timesteps 500000 \
  --base-model training/job-001/policy.zip \
  --from-experience experience/

# Use stock Gymnasium Humanoid-v4 (for comparison)
python train_locomotion.py --job-id job-003 --env-id Humanoid-v4
```

### Infrastructure (Terraform + Helm)

| Resource | Spec | Purpose |
|----------|------|---------|
| GPU node group | g5.2xlarge, 0-8, scale-from-zero | Kubeflow training jobs |
| Kubeflow namespace | Istio-injected | ML workloads isolation |
| IAM role (IRSA) | S3 read/write on models + training-data | Secure artifact access |
| S3 lifecycle | experience/ → Glacier 30d → delete 180d | Cost management |
| Katib | Bayesian optimization, 20 trials × 3 parallel | Hyperparameter search |

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
platform/
+-- cmd/                        # Service entry points
|   +-- api/                    # REST API + WebSocket + admin console
|   +-- ingestion/              # gRPC telemetry + Kafka command dispatch
|   +-- processor/              # Kafka consumer → Postgres/Redis/S3 + metrics aggregation
|   +-- worker/                 # Temporal worker (4 task queues: commands, deployments, billing, webhooks)
+-- admin-web/                  # React admin console (Vite + React 19 + TypeScript)
+-- internal/                   # Core Go packages
|   +-- api/                    # Thin HTTP handlers (handler, billing, admin, agent, model, etc.)
|   +-- service/                # Business logic layer
|   |   +-- robot_service.go    # Robot CRUD, fleet metrics
|   |   +-- inference_service.go # Per-robot model lookup + inference
|   |   +-- billing_service.go  # Pricing tiers, Temporal billing integration
|   |   +-- billing_temporal_service.go # Billing workflows (invoices, tier changes)
|   |   +-- admin_service.go    # Tenant + API key management
|   |   +-- model_service.go    # Model registry lifecycle
|   +-- billing/                # Shared pricing types (breaks import cycles)
|   +-- command/                # Semantic command registry (strategy pattern)
|   +-- auth/                   # JWT, API keys (DB-backed), OAuth2/OIDC, RBAC
|   +-- middleware/             # Rate limiting, quota, metrics, logging, CORS, tracing
|   +-- store/                  # PostgreSQL, Redis, S3, ClickHouse (interfaces + implementations)
|   +-- temporal/               # Client, workflows, activities, Kafka ack bridge
|   |   +-- workflows/          # Command, deployment, billing, training pipeline, webhook
|   |   +-- activities/         # Command, deployment, billing, training, inference, webhook
|   +-- config/                 # Environment-based configuration
+-- inference/                   # Python inference service (SB3 PPO policy serving)
|   +-- server.py               # SB3 PPO policy serving + instruction bias
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
../start.sh

# Platform only
docker compose up -d
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
| Temporal UI | http://localhost:8233 | Workflow execution history |
| Spark UI | http://localhost:8088 | Spark job monitoring |
| MinIO Console | http://localhost:9001 | S3 object browser (fleetos/fleetos123) |
| ClickHouse | http://localhost:8123 | Direct SQL analytics |

## License

MIT
