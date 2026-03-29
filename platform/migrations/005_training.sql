-- Training jobs and evaluations (Cyclotron-style)

CREATE TABLE IF NOT EXISTS training_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(64) NOT NULL,
    agent_id        UUID REFERENCES agents(id),
    status          VARCHAR(32) NOT NULL DEFAULT 'queued',
    algorithm       VARCHAR(64) NOT NULL DEFAULT 'PPO',
    environment     VARCHAR(128) NOT NULL DEFAULT 'Humanoid-v4',
    timesteps       BIGINT NOT NULL DEFAULT 1000000,
    device          VARCHAR(16) NOT NULL DEFAULT 'auto',
    config          JSONB DEFAULT '{}',
    metrics         JSONB DEFAULT '{}',
    artifact_url    TEXT,
    error_message   TEXT,
    initiated_by    VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_training_jobs_tenant ON training_jobs(tenant_id);
CREATE INDEX idx_training_jobs_status ON training_jobs(status);
CREATE INDEX idx_training_jobs_agent ON training_jobs(agent_id);

CREATE TABLE IF NOT EXISTS training_evaluations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(64) NOT NULL,
    job_id          UUID NOT NULL REFERENCES training_jobs(id),
    status          VARCHAR(32) NOT NULL DEFAULT 'queued',
    scenarios_total INT NOT NULL DEFAULT 100,
    scenarios_passed INT DEFAULT 0,
    pass_rate       DOUBLE PRECISION DEFAULT 0,
    metrics         JSONB DEFAULT '{}',
    results         JSONB DEFAULT '{}',
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_training_evals_tenant ON training_evaluations(tenant_id);
CREATE INDEX idx_training_evals_job ON training_evaluations(job_id);
CREATE INDEX idx_training_evals_status ON training_evaluations(status);
