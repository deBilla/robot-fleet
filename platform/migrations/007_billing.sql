-- Billing: tenants, daily usage, invoices, tier change events

CREATE TABLE IF NOT EXISTS tenants (
    id                  VARCHAR(64) PRIMARY KEY,
    name                VARCHAR(255) NOT NULL DEFAULT '',
    tier                VARCHAR(32) NOT NULL DEFAULT 'free',
    stripe_customer_id  VARCHAR(255),
    billing_email       VARCHAR(255),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tenants_tier ON tenants(tier);

-- Durable daily usage snapshots (replaces ephemeral Redis counters for billing).
CREATE TABLE IF NOT EXISTS usage_daily (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL,
    date        DATE NOT NULL,
    metric      VARCHAR(64) NOT NULL,
    count       BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, date, metric)
);
CREATE INDEX IF NOT EXISTS idx_usage_daily_tenant_date ON usage_daily(tenant_id, date);

-- Persisted invoices with line items.
CREATE TABLE IF NOT EXISTS invoices (
    id                  VARCHAR(64) PRIMARY KEY,
    tenant_id           VARCHAR(64) NOT NULL,
    period_start        DATE NOT NULL,
    period_end          DATE NOT NULL,
    tier                VARCHAR(32) NOT NULL,
    line_items          JSONB NOT NULL DEFAULT '{}',
    subtotal            NUMERIC(12,4) NOT NULL DEFAULT 0,
    total               NUMERIC(12,4) NOT NULL DEFAULT 0,
    currency            VARCHAR(3) NOT NULL DEFAULT 'USD',
    status              VARCHAR(32) NOT NULL DEFAULT 'draft',
    payment_intent_id   VARCHAR(255),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    paid_at             TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_invoices_tenant_period ON invoices(tenant_id, period_start);

-- Audit trail for subscription tier changes.
CREATE TABLE IF NOT EXISTS tier_change_events (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           VARCHAR(64) NOT NULL,
    from_tier           VARCHAR(32) NOT NULL,
    to_tier             VARCHAR(32) NOT NULL,
    effective_at        TIMESTAMPTZ NOT NULL,
    proration_amount    NUMERIC(12,4),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tier_changes_tenant ON tier_change_events(tenant_id, created_at);

-- Seed dev tenants (match hardcoded dev API keys in auth.go)
INSERT INTO tenants (id, name, tier) VALUES
    ('tenant-dev', 'Development Admin', 'enterprise'),
    ('tenant-demo', 'Demo Viewer', 'free')
ON CONFLICT (id) DO NOTHING;
