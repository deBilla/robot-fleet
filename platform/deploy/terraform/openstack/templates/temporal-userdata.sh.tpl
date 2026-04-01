#!/bin/bash
set -euo pipefail

POSTGRES_HOST="${postgres_host}"
POSTGRES_DB="${postgres_db}"
POSTGRES_USER="${postgres_user}"
POSTGRES_PWD="${postgres_password}"

# Install Docker
apt-get update -y
apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
apt-get update -y
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

systemctl enable docker
systemctl start docker

# Run Temporal server + UI via Docker Compose
mkdir -p /opt/temporal
cat > /opt/temporal/docker-compose.yml <<COMPOSE
services:
  temporal:
    image: temporalio/auto-setup:1.25
    restart: always
    ports:
      - "7233:7233"
      - "8000:8000"
    environment:
      - DB=postgres12
      - DB_PORT=5432
      - POSTGRES_USER=$${POSTGRES_USER}
      - POSTGRES_DB=$${POSTGRES_DB}
      - POSTGRES_SEEDS=$${POSTGRES_HOST}
      - POSTGRES_PWD=$${POSTGRES_PWD}
      - PROMETHEUS_ENDPOINT=0.0.0.0:8000
    healthcheck:
      test: ["CMD", "tctl", "--address", "localhost:7233", "cluster", "health"]
      interval: 30s
      timeout: 10s
      retries: 5

  temporal-ui:
    image: temporalio/ui:2.31.2
    restart: always
    ports:
      - "8233:8080"
    environment:
      - TEMPORAL_ADDRESS=temporal:7233
    depends_on:
      temporal:
        condition: service_healthy
COMPOSE

cd /opt/temporal && docker compose up -d
