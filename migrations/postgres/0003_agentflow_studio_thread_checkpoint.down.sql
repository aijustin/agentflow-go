DROP INDEX IF EXISTS agentflow_run_checkpoint_history_run_recorded_idx;
DROP TABLE IF EXISTS agentflow_run_checkpoint_history;

DROP INDEX IF EXISTS agentflow_run_snapshots_parent_idx;
DROP INDEX IF EXISTS agentflow_run_snapshots_thread_idx;

ALTER TABLE agentflow_run_snapshots
  DROP COLUMN IF EXISTS fork_from_version,
  DROP COLUMN IF EXISTS thread_id,
  DROP COLUMN IF EXISTS parent_run_id;
