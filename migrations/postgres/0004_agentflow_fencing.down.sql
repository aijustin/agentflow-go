DELETE FROM agentflow_schema_migrations WHERE version = 4;

ALTER TABLE agentflow_run_snapshots
  DROP COLUMN IF EXISTS fence_token;

-- The version table itself was introduced by 0004; rolling back past it
-- removes the bookkeeping table too.
DROP TABLE IF EXISTS agentflow_schema_migrations;
