-- Webhook registration and delivery tracking (P2-16)

CREATE TABLE IF NOT EXISTS webhooks (
    id          VARCHAR(64) PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL,
    url         TEXT NOT NULL,
    events      TEXT[] NOT NULL,  -- e.g. {'command.completed','deployment.finished','anomaly.detected'}
    secret      VARCHAR(256),     -- HMAC signing secret for payload verification
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhooks_tenant ON webhooks(tenant_id);
CREATE INDEX idx_webhooks_active ON webhooks(active) WHERE active = TRUE;

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id          BIGSERIAL PRIMARY KEY,
    webhook_id  VARCHAR(64) NOT NULL REFERENCES webhooks(id),
    event_type  VARCHAR(64) NOT NULL,
    payload     JSONB NOT NULL,
    status_code INT,
    attempts    INT NOT NULL DEFAULT 0,
    success     BOOLEAN NOT NULL DEFAULT FALSE,
    last_error  TEXT,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_deliveries_webhook ON webhook_deliveries(webhook_id, created_at DESC);
