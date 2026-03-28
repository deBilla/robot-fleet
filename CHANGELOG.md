# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-03-28

### Added

#### Core Services
- **Robot Simulator** (`cmd/simulator/`) — simulates N humanoid robots with 20-DOF joints, random walk navigation, battery management, and multi-modal sensor output (joint states, LiDAR, video, audio)
- **Ingestion Service** (`cmd/ingestion/`) — gRPC server that receives bidirectional telemetry streams from robots and publishes to Kafka with partition affinity by robot ID
- **API Server** (`cmd/api/`) — REST API with health check, robot management, command dispatch, telemetry queries, AI inference stub, fleet metrics, and usage reporting

#### Data Pipeline
- gRPC bidirectional streaming for robot-to-cloud telemetry (Protocol Buffers)
- Kafka producer with batching and hash-based partitioning by robot ID
- Kafka consumer with configurable consumer groups
- Redis pub/sub for real-time command delivery and WebSocket fanout

#### API & Authentication
- REST API with OpenAPI 3.1 specification (`docs/openapi.yaml`)
- JWT token authentication with configurable secret and issuer
- API key authentication with constant-time comparison
- Role-based access control (admin, operator, viewer, developer)
- Per-tenant sliding window rate limiting via Redis sorted sets
- API usage metering with daily counters for billing
- WebSocket endpoint for live telemetry streaming
- CORS middleware for development

#### Storage
- PostgreSQL schema with tables for robots, telemetry events, API usage, API keys, and model registry
- Redis hot state caching for current robot positions with TTL
- Redis-backed rate limiting and usage counters
- MinIO (S3-compatible) for cold telemetry storage

#### Infrastructure
- Docker Compose stack with PostgreSQL 16, Redis 7, Kafka (Confluent 7.6), Prometheus, Grafana, and MinIO
- Multi-stage Dockerfiles for all three services
- Helm chart with HPA autoscaling, health probes, Ingress, and secrets management
- Terraform configuration for AWS (EKS with GPU nodes, RDS, ElastiCache, S3, VPC)
- Makefile with build, test, lint, docker, helm, and terraform targets

#### Observability
- Prometheus scrape configuration for all services
- Grafana dashboard with fleet overview, API latency, Kafka lag, inference metrics, and per-tenant usage panels
- Alerting rules for robot offline, API errors, Kafka lag, inference latency, and GPU utilization

#### CI/CD
- GitHub Actions workflow with lint, test (with Postgres/Redis services), Docker build, and staging deployment
- Helm-based deployment with smoke tests

#### Testing
- 48 unit and integration tests across all packages with race detection
- Robot physics simulation tests (movement, battery, joints, charging)
- Authentication tests (JWT generation/validation, API keys, middleware, RBAC)
- Configuration tests (defaults, env overrides, fallbacks)
- HTTP middleware tests (logging, CORS, response tracking)
- API handler tests (JSON serialization, input validation)
- Integration tests (gRPC streaming end-to-end, protobuf roundtrip)
- Benchmarks for robot simulation and telemetry generation

#### Documentation
- CLAUDE.md with project guide for AI-assisted development
- README.md with architecture, quick start, API reference, and deployment guides
- OpenAPI 3.1 specification for all REST endpoints
- CHANGELOG following Keep a Changelog format

### Technical Decisions
- **Go** for all backend services — high performance, excellent concurrency, strong gRPC ecosystem
- **Protocol Buffers** for telemetry — efficient binary serialization for high-throughput sensor data
- **Kafka** for ingestion — durable, partitioned event streaming with consumer group support
- **Redis** for hot state — sub-millisecond reads for current robot positions and rate limiting
- **PostgreSQL** for cold storage — ACID transactions for robot registry and usage records
- **Helm** for Kubernetes — templated deployments with environment-specific configuration

[Unreleased]: https://github.com/dimuthu/robot-fleet/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dimuthu/robot-fleet/releases/tag/v0.1.0
