-- Fencing tokens for multi-node run-lease safety, plus the schema version
-- table used by the migration runner and startup validation.

CREATE TABLE IF NOT EXISTS agentflow_schema_migrations (
  version integer PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);

-- fence_token records the highest run-lease fencing token that has written
-- this snapshot. SaveFenced updates only match when fence_token <= the
-- caller's token, so a superseded lease holder cannot overwrite a newer
-- holder's state. Existing rows keep 0 and accept the first fenced write
-- from any token; no data backfill is needed.
ALTER TABLE agentflow_run_snapshots
  ADD COLUMN IF NOT EXISTS fence_token bigint NOT NULL DEFAULT 0;

INSERT INTO agentflow_schema_migrations (version) VALUES (4)
ON CONFLICT (version) DO NOTHING;
