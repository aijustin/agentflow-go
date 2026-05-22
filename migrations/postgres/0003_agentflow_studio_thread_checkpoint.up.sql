ALTER TABLE agentflow_run_snapshots
  ADD COLUMN IF NOT EXISTS parent_run_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS thread_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS fork_from_version bigint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS agentflow_run_snapshots_thread_idx
ON agentflow_run_snapshots (thread_id, updated_at DESC)
WHERE thread_id <> '';

CREATE INDEX IF NOT EXISTS agentflow_run_snapshots_parent_idx
ON agentflow_run_snapshots (parent_run_id)
WHERE parent_run_id <> '';

CREATE TABLE IF NOT EXISTS agentflow_run_checkpoint_history (
  run_id text NOT NULL,
  version bigint NOT NULL,
  status text NOT NULL,
  current_node_id text NOT NULL DEFAULT '',
  step_count integer NOT NULL DEFAULT 0,
  snapshot_json jsonb NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, version)
);

CREATE INDEX IF NOT EXISTS agentflow_run_checkpoint_history_run_recorded_idx
ON agentflow_run_checkpoint_history (run_id, recorded_at DESC);
