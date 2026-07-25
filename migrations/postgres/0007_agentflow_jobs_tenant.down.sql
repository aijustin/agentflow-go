DROP INDEX IF EXISTS agentflow_jobs_tenant_state_idx;

ALTER TABLE agentflow_jobs
    DROP COLUMN IF EXISTS tenant_id;
