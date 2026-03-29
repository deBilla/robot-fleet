.PHONY: all build test proto run docker-build compose-up compose-down clean

all: proto build

# --- Build ---
build:
	go build -o bin/simulator ./cmd/simulator

# --- Protobuf ---
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		-I. proto/telemetry.proto
	mv proto/telemetry*.pb.go internal/telemetry/ 2>/dev/null || true
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		-I. proto/simulation.proto
	mv proto/simulation*.pb.go internal/simulation/ 2>/dev/null || true

# --- Test ---
test:
	go test -v -race ./...

# --- Run locally ---
run:
	go run ./cmd/simulator -robots 5 -target localhost:50051

# --- Docker ---
docker-build:
	docker build -t fleetos/simulator:latest -f deploy/Dockerfile.simulator .
	docker build -t fleetos/inference:latest -f deploy/Dockerfile.inference .

compose-up:
	docker compose up -d

compose-down:
	docker compose down

# --- Clean ---
clean:
	rm -rf bin/
