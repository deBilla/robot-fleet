-- FleetOS initial schema

CREATE TABLE IF NOT EXISTS robots (
    id              VARCHAR(64) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    model           VARCHAR(128) NOT NULL DEFAULT 'humanoid-v1',
    status          VARCHAR(32) NOT NULL DEFAULT 'idle',
    pos_x           DOUBLE PRECISION NOT NULL DEFAULT 0,
    pos_y           DOUBLE PRECISION NOT NULL DEFAULT 0,
    pos_z           DOUBLE PRECISION NOT NULL DEFAULT 0,
    battery_level   DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    last_seen       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    registered_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    tenant_id       VARCHAR(64) NOT NULL,
    metadata        JSONB DEFAULT '{}'
);

CREATE INDEX idx_robots_tenant ON robots(tenant_id);
CREATE INDEX idx_robots_status ON robots(status);
CREATE INDEX idx_robots_last_seen ON robots(last_seen);

CREATE TABLE IF NOT EXISTS api_usage (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL,
    endpoint    VARCHAR(255) NOT NULL,
    method      VARCHAR(10) NOT NULL,
    status_code INT NOT NULL,
    latency_ms  BIGINT NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_tenant ON api_usage(tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS api_keys (
    key_hash    VARCHAR(128) PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL,
    name        VARCHAR(255) NOT NULL,
    role        VARCHAR(32) NOT NULL DEFAULT 'developer',
    rate_limit  INT NOT NULL DEFAULT 100,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMP WITH TIME ZONE,
    revoked     BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX idx_apikeys_tenant ON api_keys(tenant_id);

CREATE TABLE IF NOT EXISTS model_registry (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    version     VARCHAR(64) NOT NULL,
    artifact_url VARCHAR(1024) NOT NULL,
    status      VARCHAR(32) NOT NULL DEFAULT 'staged', -- staged, canary, deployed, archived
    metrics     JSONB DEFAULT '{}',
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deployed_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(name, version)
);
