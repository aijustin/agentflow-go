ALTER TABLE agentflow_runtime_events
    ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS agentflow_runtime_events_tenant_run_idx
    ON agentflow_runtime_events (tenant_id, run_id);

CREATE INDEX IF NOT EXISTS agentflow_runtime_events_tenant_time_idx
    ON agentflow_runtime_events (tenant_id, occurred_at DESC);
