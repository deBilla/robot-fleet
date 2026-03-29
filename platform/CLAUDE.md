# FleetOS - Claude Code Guide

## Project Overview

FleetOS is a distributed robot fleet management platform. It simulates humanoid robots, ingests their telemetry via gRPC/Kafka, serves a REST/WebSocket API with auth and billing, and provides AI inference capabilities.

## Architecture

```
Simulated Robots (Go) → gRPC → Ingestion Service → Kafka → Storage (Postgres/Redis/S3)
                                                              ↓
                                                     REST API ← Developer SDKs
                                                     WebSocket ← Real-time dashboards
                                                     AI Inference ← Ray Serve on GPU nodes
                                                     Istio Service Mesh (mTLS)
```

Five services:
- **simulator** (`cmd/simulator/`) — emits telemetry for N humanoid robots
- **ingestion** (`cmd/ingestion/`) — gRPC server → Kafka producer
- **api** (`cmd/api/`) — REST API + WebSocket + auth + rate limiting + billing
- **processor** (`cmd/processor/`) — Kafka consumer → Postgres/Redis
- **inference** (`inference/`) — Python diffusion policy pipeline (GR00T-N1 compatible)

## Quick Commands

```bash
# Build all services
make build

# Run tests (requires no external services)
go test -race ./internal/... ./test/...

# Run with race detection + coverage
go test -race -coverprofile=coverage.out ./...

# Lint
make lint

# Run locally with Docker Compose (starts Postgres, Redis, Kafka, Prometheus, Grafana)
docker compose up -d

# Run individual services locally
make run-ingestion   # starts gRPC server on :50051
make run-api         # starts HTTP server on :8080
make run-simulator   # starts 5 simulated robots pointing at localhost:50051

# Generate protobuf code after editing .proto files
make proto

# Build Docker images
make docker-build

# Deploy to Kubernetes
make helm-install
```

## Code Organization

| Path | Purpose |
|------|---------|
| `cmd/` | Service entry points (main.go files) |
| `internal/simulator/` | Robot physics simulation, fleet management |
| `internal/ingestion/` | gRPC telemetry handler, Kafka producer/consumer |
| `internal/api/` | Thin HTTP handlers + WebSocket (no business logic) |
| `internal/service/` | Business logic layer (accepts interfaces, owns domain logic) |
| `internal/command/` | Semantic command registry (strategy pattern) |
| `internal/auth/` | JWT + API key auth, RBAC middleware |
| `internal/middleware/` | Rate limiting, usage metering, logging, CORS, Prometheus |
| `internal/store/` | PostgreSQL and Redis data access (implements `RobotRepository`, `CacheStore` interfaces) |
| `internal/config/` | Env-based configuration |
| `internal/telemetry/` | Generated protobuf code (telemetry.proto) |
| `internal/models/` | Generated protobuf code (api.proto) |
| `proto/` | Protobuf definitions (source of truth) |
| `migrations/` | PostgreSQL DDL migrations |
| `deploy/` | Docker, Helm, Terraform, Kubernetes configs |
| `observability/` | Prometheus, Grafana, alerting configs |
| `inference/` | Python inference service (diffusion policy, GR00T-N1 compatible) |
| `sdk/python/` | Python SDK (zero-dependency, typed) |
| `sdk/typescript/` | TypeScript SDK (typed interfaces) |
| `ros2_bridge/` | ROS 2 bridge node (Python, publishes to standard topics) |
| `test/integration/` | Integration tests (gRPC, HTTP, WebSocket) |
| `docs/` | OpenAPI spec, architecture diagrams |

## Go Best Practices (MUST FOLLOW)

### Architecture — Layered & Clean

- **Handler → Service → Repository** pattern. No exceptions.
  - `internal/api/` — thin HTTP adapters: parse request, call service, write response. NO business logic.
  - `internal/service/` — business logic layer. Accepts interfaces, never concrete types.
  - `internal/store/` — data access via `RobotRepository` and `CacheStore` interfaces (`internal/store/interfaces.go`).
  - `internal/command/` — command registry (strategy pattern for extensible command parsing).
- **Dependency Injection**: All dependencies injected via constructors. No global state except Prometheus metrics.
- **Interfaces**: Define at the consumer, not the provider. Accept interfaces, return structs.
- **One file, one responsibility**: Split types/DTOs, interfaces, and implementations into separate files. A service package should have `types.go` (DTOs + interface), then one file per logical domain (e.g., `robot_service.go`, `command_service.go`, `inference_service.go`).

### SOLID Principles

- **Single Responsibility**: Each struct/file owns one concern. A handler doesn't aggregate metrics. A service doesn't format HTTP responses.
- **Open/Closed**: Use registries and interfaces for extensibility (see `internal/command/`). Add new commands by registering matchers, not editing switch statements.
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
- **Config**: All config via environment variables with sensible defaults (see `internal/config/`).
- **Protobuf**: Edit `.proto` files, then run `make proto` to regenerate Go code.

### Concurrency

- **Goroutine lifecycle**: Every goroutine must respect `ctx.Done()` for clean shutdown. Use `select` with context, never bare `for` loops on channels.
- **Mutex discipline**: Document thread-safety assumptions. Prefer `sync.RWMutex` for read-heavy data.
- **No goroutine leaks**: Every `go func()` must have a guaranteed exit path (context cancellation, channel close, or error).

## Testing (MANDATORY — TDD)

**Every code change MUST follow Test-Driven Development:**

1. **Write the test FIRST** — before writing or modifying any implementation code.
2. **Red → Green → Refactor**: Confirm the test fails, write minimal code to pass, then clean up.
3. **Run the full suite after every change**: `go test -race ./internal/... ./test/...`
4. **No untested code ships**. If you add a function, it has a test. If you fix a bug, the test proves it.

### Test patterns

- **Table-driven tests** with `testing.T` subtests for parameterized cases.
- **Unit tests** live alongside the code (`*_test.go` in the same package).
- **Integration tests** in `test/integration/` — gRPC streaming, HTTP endpoints, WebSocket.
- **Mock via interfaces**: Services accept interfaces; tests inject mock implementations. Never mock what you don't own — wrap external dependencies behind interfaces first.
- **Use `-race` flag** for all test runs. No exceptions.
- **Benchmarks**: `go test -bench=. ./internal/simulator/`
- All tests run without external services (Redis, Postgres, Kafka) unless explicitly noted.

### Required test coverage (every package must have tests)

- `internal/middleware/` — rate limit logic, fail-closed behavior on Redis errors
- `internal/api/` — WebSocket auth, connection lifecycle, handler error paths
- `internal/service/` — all 8 service methods with mock repo + mock cache
- `internal/command/` — all 12 command matchers + fallback
- `test/integration/` — HTTP inference endpoint, WebSocket streaming, gRPC telemetry
- `sdk/python/` — pytest against mock HTTP server
- `sdk/typescript/` — Jest/Vitest tests against mock HTTP

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
- **Alerting**: If you add a new failure mode, add a corresponding alert rule in `observability/alerting/alerts.yml`.

### Infrastructure as Code

- **Terraform**: All cloud resources must be in `deploy/terraform/`. No manual AWS console changes.
- **Helm**: Service configuration via `values.yaml`. Environment-specific overrides only. Every service MUST have a Helm template in `deploy/helm/fleetos/templates/`.
- **Docker**: Multi-stage builds. Minimal base images (alpine). No secrets in images.
- **Kubernetes**: HPA for autoscaling. PodDisruptionBudgets for availability. Resource requests and limits on every pod.

### Service Mesh (Istio)

- **mTLS STRICT** between all services. Every Helm template must include Istio sidecar annotations.
- **Istio manifests required**: `VirtualService`, `DestinationRule`, `PeerAuthentication` for each service.
- **Canary deployments**: Use Istio `VirtualService` weight-based routing. Configured via Helm values.
- **Circuit breakers**: Configure `DestinationRule` outlier detection for external calls (inference, Kafka).
- **Retry policies**: Configure retries for idempotent endpoints only (GET). Never retry POST commands.
- All Istio resources live in `deploy/helm/fleetos/templates/istio/`.

### Model Registry

- The `model_registry` table exists in `migrations/001_init.sql` — it MUST have corresponding Go code.
- Models have lifecycle: `staged → canary → deployed → archived`.
- Model versions are stored with `artifact_url` (S3 path), `metrics` (JSONB), and deployment timestamps.
- The inference service loads models from S3 via the registry API.

### SDK Requirements

- SDKs in `sdk/python/` and `sdk/typescript/` MUST have tests (`sdk/python/test_fleetos.py`, `sdk/typescript/fleetos.test.ts`).
- SDKs must match the OpenAPI spec in `docs/openapi.yaml`.
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

## API Authentication

Two methods:
1. **API Key**: `X-API-Key: dev-key-001` header (dev keys in `internal/auth/auth.go`)
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
