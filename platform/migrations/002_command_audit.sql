-- Command audit log for full traceability (NFR7)
-- Immutable append-only log of all commands and their lifecycle events.

CREATE TABLE IF NOT EXISTS command_audit (
    id              BIGSERIAL PRIMARY KEY,
    command_id      VARCHAR(64) NOT NULL,
    robot_id        VARCHAR(64) NOT NULL,
    tenant_id       VARCHAR(64) NOT NULL,
    command_type    VARCHAR(64) NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    status          VARCHAR(32) NOT NULL,  -- requested, dispatched, acknowledged, completed, failed
    instruction     TEXT,                   -- original natural language (for semantic commands)
    idempotency_key VARCHAR(128),
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmd_audit_robot ON command_audit(robot_id, created_at DESC);
CREATE INDEX idx_cmd_audit_tenant ON command_audit(tenant_id, created_at DESC);
CREATE INDEX idx_cmd_audit_command_id ON command_audit(command_id);
CREATE INDEX idx_cmd_audit_status ON command_audit(status);
