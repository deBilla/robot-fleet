# FleetOS Platform Architecture

FleetOS is a distributed robot fleet management platform following the **Menlo OS pattern**: the platform (cloud) handles all reasoning and intelligence; robots handle execution only.

## System Overview

```
CLIENTS                           PLATFORM (5 Services)                    PLAYGROUND
+--------------------------+      +----------------------------------------+      +------------------+
| Admin Dashboard (React)  |      |                                        |      |                  |
| Python/TypeScript SDKs   |----->| API (:8080)                            |      |                  |
| REST + WebSocket         |      | REST + WebSocket + Auth + Rate Limit   |      |                  |
+--------------------------+      +----+--------+--------+--------+--------+      |                  |
                                       |        |        |        |               |                  |
                                  Temporal   Kafka    Redis    HTTP               |                  |
                                       |        |     pub/sub    |               |                  |
                                  +----v--+ +---v----+    | +----v-----------+   |                  |
                                  |Worker | |Process-|    | | Inference      |   |                  |
                                  |Tempor-| |or      |    | | (:8081)        |   |                  |
                                  |al     | |Kafka   |    | | SB3 PPO policy |   |                  |
                                  +---+---+ +--+--+--+    | +----------------+   |                  |
                                      |       |  |  |     |                      |                  |
                                  +---v-------v--v--v-----v------------------+   |                  |
                                  | INFRASTRUCTURE                           |   |                  |
                                  | Postgres - Redis - Kafka - S3/MinIO      |   |                  |
                                  | Temporal - Prometheus - Grafana           |   |                  |
                                  | ClickHouse (optional OLAP)               |   |                  |
                                  +------------------------------------------+   |                  |
                                                                                  |                  |
                                  +------------------------------------------+   |                  |
                                  | Ingestion (:50051)                       |   |                  |
                                  | gRPC bridge - Robot <-> Platform gateway |<->| MuJoCo Simulator |
                                  +------------------------------------------+   +------------------+
```

## Services

### 1. API Service (`cmd/api/`)

The front door for all external-facing traffic.

- REST endpoints for robots, models, agents, training, billing, safety, webhooks, analytics
- WebSocket `/api/v1/ws/telemetry` subscribes to Redis pub/sub and streams live robot state to dashboards
- Authentication: JWT Bearer tokens + API keys with RBAC (admin/operator/viewer/developer)
- Middleware stack: rate limiting (Redis-backed, fail-closed), usage metering, quota enforcement, CORS, audit logging, Prometheus metrics
- Admin SPA console at `/admin/` (Vite React app)

### 2. Ingestion Service (`cmd/ingestion/`)

The only service robots communicate with directly.

- gRPC `StreamTelemetry()`: robots send telemetry packets, published to Kafka topic `robot.telemetry`
- gRPC `StreamCommands()`: robots subscribe to commands (Kafka primary path, Redis pub/sub fallback)
- `CommandDispatcher` maintains per-robot channels, routing dispatched commands from Kafka to the correct gRPC stream
- Publishes robot acknowledgements to `robot.command-acks` Kafka topic

### 3. Processor Service (`cmd/processor/`)

Kafka stream processor that fans out telemetry to multiple storage backends.

- Consumes `robot.telemetry` and writes to:
  - **Redis**: hot state hash per robot (5-min TTL) + pub/sub for WebSocket fanout
  - **S3/MinIO**: raw telemetry (NDJSON batches flushed every 100 records or 30s), video, LiDAR, audio files
  - **Postgres**: robot metadata upsert (throttled to every 50th packet per robot)
- Consumes `robot.commands` and starts Temporal command workflows
- Background aggregator: computes per-model success rates every 30 seconds for canary deployment decisions

### 4. Worker Service (`cmd/worker/`)

Temporal worker that executes all durable workflows across 5 task queues:

| Task Queue | Workflow | What It Does |
|------------|----------|--------------|
| `fleetos-commands` | `CommandDispatchWorkflow` | Audit log, optional inference, dispatch command, wait for robot ack (60s timeout) |
| `fleetos-deployments` | `ModelDeploymentWorkflow`, `AgentDeploymentWorkflow` | Canary rollout (X% of fleet), monitor success rate, full rollout if passing |
| `fleetos-billing` | `BillingCycleWorkflow` | Aggregate usage every 6h, generate invoice at month-end, Stripe payment, dunning retries |
| `fleetos-webhooks` | `WebhookFanoutWorkflow`, `WebhookDeliverWorkflow` | Fan out events to registered URLs with HMAC signatures and retries |
| `fleetos-training` | `TrainingPipelineWorkflow` | Submit K8s training job, poll status, evaluate, auto-deploy model if passing |

### 5. Inference Service (`platform/inference/`)

AI brain deployed in the platform stack.

- Loads SB3 PPO policy from S3/MinIO
- `POST /predict`: takes observation vector + instruction, returns 17-dim action vector
- Maps MuJoCo actuators (17) to Fleet joint schema (20)
- Called by API service directly or by Worker via Temporal activity
- Protected by circuit breaker (5 failure threshold, 30s timeout)

## Kafka Topics

| Topic | Producer | Consumer | Purpose |
|-------|----------|----------|---------|
| `robot.telemetry` | Ingestion | Processor | Raw telemetry packets (partitioned by robot_id) |
| `robot.commands` | API | Processor | New commands, triggers Temporal workflows |
| `robot.commands.dispatch` | Worker | Ingestion | Resolved commands for robot delivery |
| `robot.command-acks` | Ingestion | Worker | Robot acknowledgements, signals running workflows |
| `robot.telemetry.dlq` | Processor | Manual | Failed deserialization packets |
| `deployment.events` | Worker | API (SSE) | Deployment progress for dashboard streaming |

## Storage Architecture

### Redis (Hot State)

Real-time reads and WebSocket push. Millisecond freshness.

| Key Pattern | Purpose | TTL |
|-------------|---------|-----|
| `robot:state:{robot_id}` | Current robot state (joints, battery, position, metrics) | 5 min |
| `rate_limit:{tenant_id}:{endpoint}` | Sliding window rate limit counters | Short |
| `usage_counter:{tenant_id}:{metric}:{date}` | Daily usage counters for billing | 48h |
| `command_dedup:{dedup_key}` | Command idempotency cache | 24h |
| Channel `telemetry:all` | Pub/sub for all robot state updates | N/A |
| Channel `telemetry:{robot_id}` | Pub/sub for specific robot updates | N/A |
| Channel `commands:{robot_id}` | Legacy command delivery fallback | N/A |

### PostgreSQL (Durable State)

Queryable persistent storage. Updated every ~5 seconds per robot (throttled).

| Table | Purpose |
|-------|---------|
| `robots` | Fleet registry with last known state (id, name, model, status, position, battery, tenant_id) |
| `model_registry` | ML model lifecycle: staged, canary, deployed, archived (artifact_url, metrics JSONB) |
| `command_audit` | Full command trail with idempotency keys and status tracking |
| `agents` | Agent payloads with safety envelopes and motor skill dependencies |
| `deployments` | Deployment records (strategy, canary %, target fleet, validation report) |
| `deployment_events` | SSE streaming events for deployment progress |
| `training_jobs` | Training pipeline state (algorithm, timesteps, device, metrics) |
| `training_evals` | Evaluation results (scenarios passed/total, pass rate) |
| `tenants` | Subscription tiers: free, pro, enterprise (Stripe customer ID) |
| `usage_daily` | Durable usage snapshots aggregated from Redis counters |
| `invoices` | Generated invoices with line items, payment status, Stripe intent ID |
| `tier_change_events` | Audit trail for subscription tier changes with proration |
| `api_keys` | Hashed API keys with per-tenant rate limits and expiry |
| `api_usage` | Per-call metering for monitoring |
| `safety_incidents` | Safety audit trail with telemetry snapshots |
| `skills_catalog` | Motor skill definitions (required joints, skill type) |

### S3/MinIO (Archive)

Bulk storage for raw telemetry and ML artifacts. Batched writes (30s flush interval).

```
s3://fleetos-telemetry/
  telemetry/{YYYY/MM/DD/HH}/{robot_id}/batch_{timestamp}.ndjson
  video/{YYYY/MM/DD/HH}/{robot_id}/{timestamp}.jpeg
  lidar/{YYYY/MM/DD/HH}/{robot_id}/{timestamp}.pb
  audio/{YYYY/MM/DD/HH}/{robot_id}/{timestamp}.pcm

s3://fleetos-models/
  models/humanoid-v4-ppo/model.pkl
  models/humanoid-v4-ppo/vec_normalize_stats.pkl
```

### ClickHouse (Optional OLAP)

Analytics queries over fleet-wide time-series data. Graceful fallback when unavailable.

## Data Flows

### Telemetry: Robot to Dashboard

```
Simulator (50 Hz physics)
  │ every 100ms (10 Hz)
  ▼
TelemetryClient (gRPC StreamTelemetry)
  │ protobuf TelemetryPacket
  ▼
Ingestion Service (:50051)
  │ marshal + publish
  ▼
Kafka (robot.telemetry, partitioned by robot_id)
  │
  ▼
Processor Service
  ├──> Redis: hot state hash (every packet)
  │      └──> Redis pub/sub: telemetry:all channel
  │              └──> API WebSocket: push to browser
  ├──> S3: NDJSON batch (every 100 records or 30s)
  │    S3: raw video/lidar/audio files
  └──> Postgres: robots table upsert (every 50th packet)
```

### Command: User to Robot

```
POST /api/v1/robots/{id}/command
  │
  ▼
API Handler (auth + validate)
  │
  ▼
RobotService.SendCommand()
  │ compute dedup key, publish to Kafka
  ▼
Kafka (robot.commands)
  │
  ▼
Processor (starts Temporal workflow)
  │
  ▼
Temporal: CommandDispatchWorkflow
  │ 1. Write audit (status: requested)
  │ 2. Optional: call inference (:8081/predict)
  │ 3. Publish to Kafka (robot.commands.dispatch)
  │ 4. Update audit (status: dispatched)
  │ 5. Wait for signal "command-ack" (60s timeout)
  │
  ▼
Kafka (robot.commands.dispatch)
  │
  ▼
Ingestion: CommandDispatcher
  │ route to per-robot gRPC channel
  ▼
gRPC StreamCommands() -> Robot
  │ protobuf RobotCommand
  ▼
Simulator: CommandHandler
  │ translate to MuJoCo torques
  │ execute on physics engine
  │
  ▼ (ack path)
Ingestion -> Kafka (robot.command-acks)
  -> Worker: signal Temporal workflow
  -> Audit updated (status: acked)
```

### Model Deployment: Canary Rollout

```
POST /api/v1/models/{id}/deploy (canary: 20%)
  │
  ▼
ModelDeploymentWorkflow (Temporal)
  │ 1. Validate model artifact on S3
  │ 2. Deploy to 20% of target fleet (update robots.inference_model_id)
  │ 3. Monitor canary: processor aggregates uptime_pct per model every 30s
  │ 4. If success_rate >= 80%: deploy to remaining 80%
  │ 5. Update model_registry status: staged -> canary -> deployed
  │
  ▼
deployment_events Kafka topic -> API SSE stream -> Dashboard
```

### Billing: Monthly Cycle

```
BillingCycleWorkflow (Temporal, runs for 1 month)
  │
  │ Phase 1: every 6 hours
  │   └─ Aggregate Redis usage counters -> Postgres usage_daily
  │   └─ Handle signals: change-tier, cancel, retry-payment
  │
  │ Phase 2: month-end
  │   └─ Generate invoice from usage_daily totals
  │   └─ Process Stripe payment (3 dunning retries if failed)
  │   └─ Store invoice in Postgres
  │
  └─ ContinueAsNew -> next month's cycle
```

## Authentication

Two methods, both validated by `AuthMiddleware`:

1. **JWT Bearer**: `Authorization: Bearer <token>` (HMAC-SHA256, claims: tenant_id, role, expiry)
2. **API Key**: `X-API-Key: <key>` header (hashed in Postgres, dev keys hardcoded for local testing)

Dev keys:
- `dev-key-001` -> tenant-dev (admin)
- `dev-key-002` -> tenant-demo (viewer)

Roles: admin, operator, viewer, developer

## Middleware Stack

| Middleware | Purpose |
|------------|---------|
| Logging | Structured JSON via `log/slog` with correlation fields |
| Metrics | Prometheus counters + histograms for every endpoint |
| Rate Limiting | Redis sliding window, per-tenant, **fail-closed** if Redis is down |
| Usage Metering | Increments Redis counters per endpoint per tenant |
| Quota Enforcement | Per-tier limits (free/pro/enterprise), blocks if exceeded |
| CORS | Open in dev, restricted in prod |
| Audit Logging | Persists actions to `command_audit` table |
| Tracing | OpenTelemetry with OTLP exporter |

## Command System

Strategy pattern with keyword matchers in `internal/command/`:

| Keywords | Command Type |
|----------|-------------|
| "stop", "halt" | `stop` |
| "dance" | `dance` |
| "wave", "hello", "greet" | `wave` |
| "jump", "hop" | `jump` |
| "forward", "ahead" | `move_relative` (forward) |
| "back" | `move_relative` (backward) |
| "go to", "move to" | `move` |
| (no match) | `semantic` (sent to inference) |

New commands are added by registering matchers, not editing switch statements.

## Observability

**Prometheus metrics** (scraped from each service's `/metrics` endpoint):
- `fleetos_api_requests_total`, `fleetos_api_request_duration_seconds`
- `fleetos_robots_total`, `fleetos_robots_active`, `fleetos_robots_error`
- `fleetos_avg_battery_level`
- `fleetos_telemetry_packets_total`
- `fleetos_inference_duration_seconds`
- `fleetos_command_dispatch_duration_seconds`
- `fleetos_grpc_active_streams`, `fleetos_grpc_stream_messages_total`
- `fleetos_tenant_api_calls_total`
- `fleetos_websocket_connections`

**Grafana**: auto-provisioned dashboards from Prometheus + ClickHouse datasource

**Alerting**: rules in `observability/alerting/alerts.yml`

## Infrastructure (Docker Compose)

```
Infrastructure:
  postgres:16-alpine (:5432)      redis:7-alpine (:6379)
  kafka:confluent-7.6 (:9092)     zookeeper
  minio (:9000)                   minio-init (bucket setup)
  temporal (:7233)                temporal-ui (:8233)
  prometheus (:9090)              grafana (:3000)
  clickhouse (:8123)              spark-master + spark-worker

Application:
  api (:8080)                     ingestion (:50051)
  processor (Kafka consumer)      worker (Temporal worker)
  inference (:8081)
```

All services have health checks. Playground connects to platform via shared Docker network.

## Design Patterns

1. **Layered architecture**: Handler -> Service -> Repository. No business logic in handlers.
2. **Dependency injection**: All services accept interfaces via constructors. No global state.
3. **Temporal durable workflows**: Commands, deployments, billing all survive crashes and retries.
4. **Graceful degradation**: Every external dependency (Temporal, Kafka, ClickHouse, inference) has a fallback path.
5. **Hot/cold storage**: Redis (real-time) -> Postgres (durable) -> S3 (archive).
6. **Canary deployments**: Models go staged -> canary -> deployed with automated performance monitoring.
7. **Idempotency**: Dedup keys on commands, upserts on telemetry, Kafka replays are safe.
8. **Fail-closed security**: Rate limiting rejects requests if Redis is unavailable.
9. **Strategy pattern**: Command registry with keyword matchers, extensible without editing switch statements.
10. **Interface segregation**: Small focused interfaces (RobotRepository, CacheStore, ModelRepository, etc.) defined at the consumer.
