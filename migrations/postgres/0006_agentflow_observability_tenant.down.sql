DROP INDEX IF EXISTS agentflow_runtime_events_tenant_time_idx;
DROP INDEX IF EXISTS agentflow_runtime_events_tenant_run_idx;

ALTER TABLE agentflow_runtime_events
    DROP COLUMN IF EXISTS tenant_id;
