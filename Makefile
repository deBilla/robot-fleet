.PHONY: all build test proto docker-build docker-push compose-up compose-down helm-install lint clean

# Variables
REGISTRY ?= fleetos
TAG ?= latest
SERVICES := simulator ingestion api processor fleetos

all: proto build

# --- Build ---
build:
	@echo "Building all services..."
	@for svc in $(SERVICES); do \
		echo "  Building $$svc..."; \
		go build -o bin/$$svc ./cmd/$$svc; \
	done

build-%:
	go build -o bin/$* ./cmd/$*

# --- Protobuf ---
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		-I. proto/telemetry.proto proto/api.proto
	mv proto/*.pb.go internal/telemetry/ 2>/dev/null || true
	mv proto/*_grpc.pb.go internal/telemetry/ 2>/dev/null || true

# --- Test ---
test:
	go test -v -race ./...

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# --- Lint ---
lint:
	golangci-lint run ./...

# --- Docker ---
docker-build:
	@for svc in $(SERVICES); do \
		echo "Building docker image: $(REGISTRY)/$$svc:$(TAG)"; \
		docker build -t $(REGISTRY)/$$svc:$(TAG) -f deploy/docker/Dockerfile.$$svc .; \
	done

docker-push:
	@for svc in $(SERVICES); do \
		docker push $(REGISTRY)/$$svc:$(TAG); \
	done

# --- Docker Compose (local dev) ---
compose-up:
	docker compose up -d

compose-down:
	docker compose down

compose-logs:
	docker compose logs -f

# --- Helm ---
helm-install:
	helm upgrade --install fleetos deploy/helm/fleetos \
		--namespace fleetos --create-namespace \
		-f deploy/helm/fleetos/values.yaml

helm-template:
	helm template fleetos deploy/helm/fleetos

helm-uninstall:
	helm uninstall fleetos --namespace fleetos

# --- Run locally ---
run-ingestion:
	go run ./cmd/ingestion

run-api:
	go run ./cmd/api

run-simulator:
	go run ./cmd/simulator -robots 5 -target localhost:50051

# --- Database ---
db-migrate:
	psql "$(POSTGRES_DSN)" -f migrations/001_init.sql

# --- Clean ---
clean:
	rm -rf bin/ coverage.out coverage.html

# --- Terraform ---
tf-init:
	cd deploy/terraform && terraform init

tf-plan:
	cd deploy/terraform && terraform plan

tf-apply:
	cd deploy/terraform && terraform apply
