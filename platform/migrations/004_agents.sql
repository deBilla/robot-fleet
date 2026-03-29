-- Agent platform: agents, deployments, safety incidents, audit log

CREATE TABLE IF NOT EXISTS agents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(64) NOT NULL,
    name            VARCHAR(255) NOT NULL,
    version         VARCHAR(64) NOT NULL,
    runtime         VARCHAR(64) NOT NULL DEFAULT 'python3.11',
    entrypoint      VARCHAR(255) NOT NULL DEFAULT 'agent.py',
    artifact_url    TEXT,
    safety_envelope JSONB NOT NULL DEFAULT '{}',
    motor_skills    JSONB DEFAULT '[]',
    model_deps      JSONB DEFAULT '[]',
    status          VARCHAR(32) NOT NULL DEFAULT 'registered',
    created_by      VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, name, version)
);

CREATE INDEX idx_agents_tenant ON agents(tenant_id);
CREATE INDEX idx_agents_status ON agents(status);
CREATE INDEX idx_agents_name ON agents(tenant_id, name);

CREATE TABLE IF NOT EXISTS deployments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id            UUID NOT NULL REFERENCES agents(id),
    tenant_id           VARCHAR(64) NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'validating',
    strategy            VARCHAR(32) NOT NULL DEFAULT 'canary',
    canary_percentage   INT NOT NULL DEFAULT 5,
    target_fleet        JSONB NOT NULL DEFAULT '[]',
    safety_envelope_override JSONB,
    validation_report   JSONB,
    rollback_reason     TEXT,
    initiated_by        VARCHAR(255) NOT NULL,
    initiated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ
);

CREATE INDEX idx_deployments_agent ON deployments(agent_id);
CREATE INDEX idx_deployments_tenant ON deployments(tenant_id);
CREATE INDEX idx_deployments_status ON deployments(status);

CREATE TABLE IF NOT EXISTS deployment_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id   UUID NOT NULL REFERENCES deployments(id),
    event_type      VARCHAR(64) NOT NULL,
    event_data      JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deploy_events_deployment ON deployment_events(deployment_id, created_at);

CREATE TABLE IF NOT EXISTS safety_incidents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    robot_id        VARCHAR(64) NOT NULL,
    agent_id        UUID REFERENCES agents(id),
    deployment_id   UUID REFERENCES deployments(id),
    site_id         VARCHAR(255) NOT NULL DEFAULT '',
    incident_type   VARCHAR(64) NOT NULL,
    severity        VARCHAR(20) NOT NULL DEFAULT 'medium',
    details         JSONB DEFAULT '{}',
    telemetry_snapshot JSONB,
    resolved_at     TIMESTAMPTZ,
    resolution      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_safety_robot ON safety_incidents(robot_id, created_at DESC);
CREATE INDEX idx_safety_severity ON safety_incidents(severity, created_at DESC);
CREATE INDEX idx_safety_deployment ON safety_incidents(deployment_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(64) NOT NULL,
    actor           VARCHAR(255) NOT NULL,
    action          VARCHAR(64) NOT NULL,
    resource_type   VARCHAR(64) NOT NULL,
    resource_id     VARCHAR(255) NOT NULL,
    details         JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_tenant ON audit_log(tenant_id, created_at DESC);
CREATE INDEX idx_audit_resource ON audit_log(resource_type, resource_id);

-- Add agent tracking columns to robots table
ALTER TABLE robots ADD COLUMN IF NOT EXISTS site_id VARCHAR(255);
ALTER TABLE robots ADD COLUMN IF NOT EXISTS current_agent_id UUID REFERENCES agents(id);
ALTER TABLE robots ADD COLUMN IF NOT EXISTS current_agent_version VARCHAR(64);
