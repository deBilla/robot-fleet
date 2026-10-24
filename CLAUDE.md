# FleetOS - Claude Code Guide

## Project Overview

FleetOS is a distributed robot fleet management platform organized as a monorepo, built on a **cloud-brain architecture**: high-level reasoning happens in the platform (cloud); robots handle execution only. The platform ingests telemetry via gRPC/Kafka, runs AI inference, manages model lifecycle, and sends commands back to robots via gRPC.

## Monorepo Layout

- `platform/` — Cloud brain: Go services, inference, model registry, training, analytics, infra, SDKs
- `playground/` — Robot simulator: MuJoCo physics, telemetry sender, command executor (no reasoning)

All Go platform code lives under `platform/`. The Go module root is `platform/go.mod`.

## Architecture (Cloud Brain, Robot Body)

```
┌─ Playground (Robot) ─────────────────────────────┐
│  Simulator (MuJoCo Humanoid-v4 physics)          │
│    ├─ gRPC StreamTelemetry → ingestion:50051     │  (send sensors)
│    ├─ gRPC StreamCommands ← ingestion:50051      │  (receive actions)
│    └─ HTTP :8085 /spawn, /robots                 │  (local control)
│  Web UI → api:8080                               │  (HTTP to platform)
└──────────────────────────────────────────────────┘

┌─ Platform (Cloud Brain) ─────────────────────────┐
│  Ingestion (gRPC :50051)                         │
│    ├─ StreamTelemetry → Kafka → Processor        │
│    └─ StreamCommands ← Kafka commands.dispatch   │
│  API (:8080) — REST + WebSocket + auth + billing │
│  Inference (:8081) — SB3 PPO policy serving      │
│    ├─ Loads model from MinIO (S3)                │
│    └─ Polls model registry for deployments       │
│  Worker (Temporal) — commands, canary deploys,   │
│    training pipeline, billing, webhooks          │
│  Training: offline CQL on fleet data (Argo or    │
│    Kubeflow [+Katib]); MuJoCo = eval only → S3   │
│  Processor → Postgres/Redis/S3/ClickHouse        │
└──────────────────────────────────────────────────┘
```

Services:
- **simulator** (`playground/simulator/`) — MuJoCo physics (Python), sends telemetry (incl. training context: 86-dim obs, action, reward, episode/step, exploration) via gRPC, receives commands via gRPC; runs its assigned locomotion policy locally every control tick (edge execution, policy chosen by the platform via `GET /api/v1/robots/{id}/policy`)
- **ingestion** (`platform/cmd/ingestion/`) — gRPC server → Kafka producer; consumes `robot.commands.dispatch` from Kafka → gRPC StreamCommands
- **api** (`platform/cmd/api/`) — REST API + WebSocket + auth + rate limiting + billing; serves the admin console at `/admin/`
- **processor** (`platform/cmd/processor/`) — Kafka consumer → Postgres/Redis/S3/ClickHouse; starts Temporal command workflows
- **worker** (`platform/cmd/worker/`) — Temporal worker: CommandDispatch, ModelDeployment (canary), TrainingPipeline, BillingCycle, WebhookFanout workflows
- **inference** (`platform/inference/`) — SB3 PPO policy serving + FAISS semantic command resolution, loads models from S3
- **training** (`platform/training/`) — The Temporal pipeline trains OFFLINE from fleet experience: dataset snapshot of `training_data/v3/` → [Katib HPO] → `train_offline.py` (CQL-SAC, no simulator) as Argo template `fleetos-offline-training` or Kubeflow Trainer TrainJobs (runtime `fleetos-sb3`) → `evaluate_policy.py` on the MuJoCo test track (eval only). Online MuJoCo scripts (PPO `train_locomotion.py`, SAC+HER `train_reach.py`) remain as manual bootstrap tools; BC/autoencoder run standalone

Also: `platform/cmd/fleetos/` (cobra CLI client for the API), `platform/admin-web/` (React admin console), `playground/web/` (React + Three.js 3D dashboard), `playground/ros2_bridge/` (ROS 2 topic bridge). `playground/cmd/simulator/` + `playground/internal/` is the legacy Go kinematic simulator (`Dockerfile.simulator-go`), superseded by the MuJoCo one.

## Quick Commands

```bash
# Start both stacks from repo root
./start.sh

# Start/stop individual stacks
./start.sh --platform
./start.sh --playground
./start.sh down

# Other start.sh commands: restart, logs, ps

# Build all platform services
cd platform && make build

# Run platform tests (requires no external services)
cd platform && go test -race ./internal/... ./test/...

# Run with race detection + coverage
cd platform && go test -race -coverprofile=coverage.out ./...

# Lint
cd platform && make lint

# Generate protobuf code after editing .proto files
cd platform && make proto

# Build Docker images
cd platform && make docker-build

# Deploy to Kubernetes (Helm direct)
cd platform && make helm-install

# Deploy to Kubernetes (kind + ArgoCD — local dev, fully automated)
cd platform && make k8s-up        # Full stack: kind + ArgoCD + Argo Workflows + all images
cd platform && make k8s-status    # Pod and service status
cd platform && make k8s-sync      # Force ArgoCD re-sync after code changes
cd platform && make k8s-logs SVC=api  # Tail service logs
cd platform && make k8s-reload SVC=api  # Rebuild + reload image + restart
cd platform && make k8s-train TASK=locomotion TIMESTEPS=50000  # Submit training via Argo Workflows
cd platform && make k8s-argocd-password  # Get ArgoCD admin password
cd platform && make k8s-down      # Delete kind cluster

# Deploy to k3s on Colima (lightweight; Argo + Kubeflow Trainer + Katib)
cd platform && make k3s-up        # Colima k3s + images + Argo Workflows + Kubeflow Trainer v2.3.0 + Katib v0.19.0 + Helm
cd platform && make k3s-images    # Rebuild images + restart platform pods
cd platform && make k3s-train TASK=locomotion TIMESTEPS=20000  # Manual online (MuJoCo) training, e.g. bootstrap policy
cd platform && make k3s-down      # colima stop (MinIO PVC survives)
# Full pipeline (fleet-data snapshot → [Katib] → offline train → test-track eval → gate → register → canary):
#   POST /api/v1/training/pipelines {"task_type":"locomotion","gradient_steps":5000,"use_katib":true}
# Playground against k3s: playground/docker-compose.k3s.yml (PLATFORM_HOST=<colima VM IP>)

# K8s dashboards (all NodePort, no port-forward needed after make k8s-up):
#   ArgoCD:         http://localhost:30080
#   Argo Workflows: https://localhost:30746
#   Grafana:        http://localhost:30030  (admin/admin)
#   Prometheus:     http://localhost:30090
#   API:            http://localhost:30880

# Train locally (faster for dev iteration); --n-envs 8 ≈ 3× the single-env speed on 8+ cores.
# metrics.json carries a learning curve ("history"); final eval reward is raw test-track reward.
cd platform/training
python3 train_locomotion.py --job-id demo --timesteps 50000 --n-envs 8 --n-steps 512 \
  --env-id FleetOS-Humanoid-v2 --s3-endpoint localhost:9000
```

## Code Organization

| Path | Purpose |
|------|---------|
| `platform/cmd/` | Entry points: `api`, `ingestion`, `processor`, `worker`, `fleetos` (CLI) |
| `platform/internal/ingestion/` | gRPC telemetry handler, Kafka producer/consumer |
| `platform/internal/api/` | Thin HTTP handlers + WebSocket (no business logic) |
| `platform/internal/service/` | Business logic layer (accepts interfaces, owns domain logic) |
| `platform/internal/command/` | Semantic command registry (strategy pattern) |
| `platform/internal/auth/` | JWT + OAuth2/OIDC + API key auth, RBAC middleware |
| `platform/internal/argo/` | Argo Workflows Server REST client (submit WorkflowTemplates, poll status) |
| `platform/internal/kube/` | Minimal in-cluster Kubernetes REST client (create/get custom resources) |
| `platform/internal/kubeflow/` | Kubeflow Trainer TrainJob + Katib Experiment builders and status parsing |
| `platform/internal/modelgate/` | Promotion gate (eval pass rate + reward vs deployed baseline) |
| `platform/internal/experience/` | Fleet experience records + S3 keys (`training_data/v3/`) written by the processor, read by offline training |
| `platform/internal/temporal/` | Temporal client, workflows, activities, Kafka ack bridge |
| `platform/internal/billing/` | Pricing tiers |
| `platform/internal/circuitbreaker/` | Circuit breaker for downstream calls |
| `platform/internal/middleware/` | Rate limiting, usage metering, logging, CORS, Prometheus |
| `platform/internal/store/` | PostgreSQL, Redis, S3, ClickHouse data access (implements `RobotRepository`, `CacheStore` interfaces) |
| `platform/internal/config/` | Env-based configuration |
| `platform/internal/telemetry/` | Generated protobuf code (telemetry.proto) |
| `platform/internal/models/` | Generated protobuf code (api.proto) |
| `platform/proto/` | Protobuf definitions (source of truth) |
| `platform/migrations/` | PostgreSQL DDL migrations |
| `platform/deploy/` | Docker, Helm, Terraform, Kubernetes configs |
| `platform/observability/` | Prometheus, Grafana, alerting configs |
| `platform/sdk/python/` | Python SDK (zero-dependency, typed) |
| `platform/sdk/typescript/` | TypeScript SDK (typed interfaces) |
| `platform/training/` | Offline CQL trainer (pipeline), test-track evaluation, manual online/BC/autoencoder scripts |
| `platform/analytics/` | Spark analytics pipeline |
| `platform/docs/` | OpenAPI spec, `architecture.md` (Mermaid), `ml-pipeline.md` |
| `platform/admin-web/` | React admin console (tenants, API keys, billing) |
| `playground/simulator/` | MuJoCo robot simulator (Python) — physics + telemetry + command execution |
| `platform/inference/` | SB3 PPO inference service (Python) |
| `playground/web/` | React + Three.js 3D robot dashboard |
| `playground/ros2_bridge/` | ROS 2 bridge (JointState, PoseStamped, BatteryState, LaserScan) |
| `playground/proto/` | Protobuf definitions (playground copy, stubs generated at Docker build) |

## Go Best Practices (MUST FOLLOW)

### Architecture — Layered & Clean

- **Handler → Service → Repository** pattern. No exceptions.
  - `platform/internal/api/` — thin HTTP adapters: parse request, call service, write response. NO business logic.
  - `platform/internal/service/` — business logic layer. Accepts interfaces, never concrete types.
  - `platform/internal/store/` — data access via `RobotRepository` and `CacheStore` interfaces (`platform/internal/store/interfaces.go`).
  - `platform/internal/command/` — command registry (strategy pattern for extensible command parsing).
- **Dependency Injection**: All dependencies injected via constructors. No global state except Prometheus metrics.
- **Interfaces**: Define at the consumer, not the provider. Accept interfaces, return structs.
- **One file, one responsibility**: Split types/DTOs, interfaces, and implementations into separate files. A service package should have `types.go` (DTOs + interface), then one file per logical domain (e.g., `robot_service.go`, `command_service.go`, `inference_service.go`).

### SOLID Principles

- **Single Responsibility**: Each struct/file owns one concern. A handler doesn't aggregate metrics. A service doesn't format HTTP responses.
- **Open/Closed**: Use registries and interfaces for extensibility (see `platform/internal/command/`). Add new commands by registering matchers, not editing switch statements.
- **Liskov Substitution**: Any implementation of `RobotRepository` or `CacheStore` must be swappable without breaking callers.
- **Interface Segregation**: Keep interfaces small and focused. Don't force callers to depend on methods they don't use.
- **Dependency Inversion**: High-level modules (services) depend on abstractions (interfaces), not low-level modules (Postgres/Redis).

### Error Handling

- **Always wrap errors** with context: `fmt.Errorf("get robot %s: %w", id, err)`.
- **Return errors, don't panic**. Log at the boundary (handler), propagate everywhere else.
- **Never ignore errors silently**. If you must ignore, comment why: `_ = conn.Close() // best-effort cleanup`.
- **Fail-closed on security paths**: If Redis is down, rate limiting should reject (not allow) requests.

### Naming & Style

- **Go version**: 1.26+ (uses `range N` syntax, `log/slog`, `math/rand/v2`)
- Standard Go naming: `MixedCaps`, no underscores. Exported types have doc comments.
- **No magic numbers**: Extract to named constants with clear names (e.g., `BatteryIdleDrain`, `MaxListLimit`).
- **Logging**: Use `log/slog` with structured fields (`slog.Error("msg", "key", val)`). JSON handler in production.
- **Config**: All config via environment variables with sensible defaults (see `platform/internal/config/`).
- **Protobuf**: Edit `.proto` files, then run `make proto` to regenerate Go code.

### Concurrency

- **Goroutine lifecycle**: Every goroutine must respect `ctx.Done()` for clean shutdown. Use `select` with context, never bare `for` loops on channels.
- **Mutex discipline**: Document thread-safety assumptions. Prefer `sync.RWMutex` for read-heavy data.
- **No goroutine leaks**: Every `go func()` must have a guaranteed exit path (context cancellation, channel close, or error).

## Testing (MANDATORY — TDD)

**Every code change MUST follow Test-Driven Development:**

1. **Write the test FIRST** — before writing or modifying any implementation code.
2. **Red → Green → Refactor**: Confirm the test fails, write minimal code to pass, then clean up.
3. **Run the full suite after every change**: `cd platform && go test -race ./internal/... ./test/...`
4. **No untested code ships**. If you add a function, it has a test. If you fix a bug, the test proves it.

### Test patterns

- **Table-driven tests** with `testing.T` subtests for parameterized cases.
- **Unit tests** live alongside the code (`*_test.go` in the same package).
- **Integration tests** in `platform/test/integration/` — gRPC streaming, HTTP endpoints, WebSocket.
- **Mock via interfaces**: Services accept interfaces; tests inject mock implementations. Never mock what you don't own — wrap external dependencies behind interfaces first.
- **Use `-race` flag** for all test runs. No exceptions.
- **Benchmarks**: `cd platform && go test -bench=. ./internal/simulator/`
- All tests run without external services (Redis, Postgres, Kafka) unless explicitly noted.

### Required test coverage (every package must have tests)

- `platform/internal/middleware/` — rate limit logic, fail-closed behavior on Redis errors
- `platform/internal/api/` — WebSocket auth, connection lifecycle, handler error paths
- `platform/internal/service/` — all 8 service methods with mock repo + mock cache
- `platform/internal/command/` — all 12 command matchers + fallback
- `platform/test/integration/` — HTTP inference endpoint, WebSocket streaming, gRPC telemetry
- `platform/sdk/python/` — pytest against mock HTTP server
- `platform/sdk/typescript/` — Jest/Vitest tests against mock HTTP

## Distributed Systems Best Practices (MUST FOLLOW for infra changes)

### Data Pipeline & Messaging

- **Kafka**: Partition by entity ID (robot_id) for ordering guarantees. Use consumer groups for horizontal scaling. Handle backpressure — don't drop messages silently.
- **gRPC**: Use bidirectional streaming for telemetry. Implement proper deadlines and cancellation. Always handle `stream.Recv()` errors.
- **Idempotency**: All message handlers must be idempotent. Kafka consumer replays happen. Use upserts, not inserts.

### Resilience

- **Graceful shutdown**: All services must handle SIGTERM. Drain in-flight requests, close connections, flush buffers. Use `context.WithTimeout` for shutdown deadlines.
- **Circuit breakers**: External service calls (inference, Kafka) should fail fast when downstream is unhealthy. Don't retry indefinitely.
- **Health checks**: Every service exposes `/healthz`. Kubernetes liveness and readiness probes depend on these.
- **Timeouts everywhere**: Every external call (HTTP, gRPC, Redis, Postgres) must have an explicit timeout. No unbounded waits.

### Observability

- **Metrics**: Every new endpoint or pipeline stage gets Prometheus metrics (counter + histogram at minimum). Use `middleware.InferenceDuration`, `middleware.TelemetryPacketsTotal`, etc.
- **Structured logging**: Use `log/slog` with correlation fields (robot_id, tenant_id, request_id).
- **Alerting**: If you add a new failure mode, add a corresponding alert rule in `platform/observability/alerting/alerts.yml`.

### Infrastructure as Code

- **Terraform**: All cloud resources must be in `platform/deploy/terraform/`. No manual AWS console changes.
- **Helm**: Service configuration via `values.yaml`. Environment-specific overrides only. Every service MUST have a Helm template in `platform/deploy/helm/fleetos/templates/`.
- **Docker**: Multi-stage builds. Minimal base images (alpine). No secrets in images.
- **Kubernetes**: HPA for autoscaling. PodDisruptionBudgets for availability. Resource requests and limits on every pod.

### Service Mesh (Istio)

- **mTLS STRICT** between all services. Every Helm template must include Istio sidecar annotations.
- **Istio manifests required**: `VirtualService`, `DestinationRule`, `PeerAuthentication` for each service.
- **Canary deployments**: Use Istio `VirtualService` weight-based routing. Configured via Helm values.
- **Circuit breakers**: Configure `DestinationRule` outlier detection for external calls (inference, Kafka).
- **Retry policies**: Configure retries for idempotent endpoints only (GET). Never retry POST commands.
- All Istio resources live in `platform/deploy/helm/fleetos/templates/istio/`.

### Model Registry

- The `model_registry` table exists in `platform/migrations/001_init.sql` — it MUST have corresponding Go code.
- Models have lifecycle: `staged → canary → deployed → archived`.
- Model versions are stored with `artifact_url` (S3 path), `metrics` (JSONB), and deployment timestamps.
- The inference service loads models from S3 via the registry API; API inference requests carry the robot's assigned `artifact_url` (canary routing).
- Promotion is gated (`internal/modelgate`): `POST /models/{id}/deploy` rejects unevaluated/failing models (422) and otherwise starts the Temporal canary (`ModelDeploymentWorkflow`); only its final step (`PromoteModel`) marks a model deployed.
- Train/serve parity: robots and the test track share `observation.py`, `locomotion_reward.py`, `policy_meta.py`, `policy_action.py` and `assets/lab_room.xml` (byte-identical copies in `platform/training/` and `playground/simulator/`, enforced by `platform/training/test_parity.py`). Change both copies together.
- Actions: `policy_meta.json` records each policy's `action_version` (`policy_action.py`). 1 = absolute joint targets (default for policies without the field); 2 = residual, the locomotion trainers' default: outputs in [-1, 1] offset each joint from the standing pose by up to `RESIDUAL_SCALE` (0.5 rad). Robots and inference wrap residual policies so everything downstream, including fleet experience, sees joint targets; the offline trainer converts recorded targets back to residual actions; the test track evaluates each policy in its own version. `inference/server.py` mirrors the constants (checked by `test_parity.py`).
- Robots end episodes like the test track: a fall (torso below `FALL_Z`, 1.0 m — a crouch counts) or `MAX_EPISODE_STEPS` (1000 ticks, a truncation — no fall penalty, no terminal experience record). A robot stands up where it fell, or at its spawn point if it ended within `RESET_WALL_MARGIN` of a wall.
- Observations are egocentric (`OBS_VERSION` 3): positions relative to the torso, vectors in the robot's heading frame, yaw removed — the same pose reads the same anywhere in the room. Every trainer writes `policy_meta.json` (`{"obs_version": N}`) next to `policy.zip`; robots, inference and the test track refuse policies without it or with another version. Changing the observation layout means bumping `OBS_VERSION` (both copies + `inference/server.py`), bumping the experience prefix in `internal/experience/transition.go`, and retraining.
- Pipeline-trained models record their data lineage in `metrics` (`dataset_manifest`, `dataset_files`): the exact fleet-experience batches (`datasets/<job>/manifest.json`) they were trained on.

### SDK Requirements

- SDKs in `platform/sdk/python/` and `platform/sdk/typescript/` MUST have tests.
- SDKs must match the OpenAPI spec in `platform/docs/openapi.yaml`.
- Test SDKs against mock HTTP server, not live API.

## Environment Variables

Key variables (all have defaults for local dev):

| Variable | Default | Description |
|----------|---------|-------------|
| `GRPC_PORT` | 50051 | gRPC server port |
| `HTTP_PORT` | 8080 | REST API port |
| `KAFKA_BROKERS` | localhost:9092 | Kafka bootstrap servers |
| `POSTGRES_DSN` | postgres://fleetos:fleetos@localhost:5432/fleetos?sslmode=disable | PostgreSQL DSN |
| `REDIS_ADDR` | localhost:6379 | Redis address |
| `SIM_ROBOT_COUNT` | 10 | Number of simulated robots |
| `SIM_TICK_MS` | 100 | Simulation tick interval (ms) |
| `JWT_SECRET` | dev-secret-change-me | JWT signing secret |
| `RATE_LIMIT_RPS` | 100 | Per-tenant rate limit |
| `ARGO_SERVER_URL` | (empty = training off) | Argo Server REST API used by api + worker |
| `MODEL_MIN_EVAL_PASS_RATE` | 0.5 | Promotion gate: minimum eval scenario pass rate |
| `KUBEFLOW_ENABLED` | false | Worker submits Kubeflow TrainJobs / Katib Experiments (needs Trainer + Katib installed) |
| `HPO_ALGORITHM` | tpe | Katib algorithm (also `HPO_MAX_TRIALS`=4, `HPO_PARALLEL_TRIALS`=2, `HPO_TRIAL_GRADIENT_STEPS`=2000, `HPO_EVAL_EPISODES`=5, `KUBEFLOW_TRAINING_SECRET`=fleetos-training) |
| `TELEMETRY_BUCKET` | fleetos-telemetry | Worker: bucket holding fleet experience (`training_data/v3/`) for dataset snapshots |

## API Authentication

Two methods:
1. **API Key**: `X-API-Key: dev-key-001` header (dev keys in `platform/internal/auth/auth.go`)
2. **JWT Bearer**: `Authorization: Bearer <token>` header

Dev API keys for local testing:
- `dev-key-001` → tenant-dev (admin)
- `dev-key-002` → tenant-demo (viewer)

## Dependencies

External services needed for full stack (all provided via docker-compose):
- PostgreSQL 16
- Redis 7
- Kafka (Confluent 7.6)
- Prometheus + Grafana (observability)
- MinIO (S3-compatible storage)
