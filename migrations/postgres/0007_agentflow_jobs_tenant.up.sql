ALTER TABLE agentflow_jobs
    ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS agentflow_jobs_tenant_state_idx
    ON agentflow_jobs (tenant_id, state, created_at);
