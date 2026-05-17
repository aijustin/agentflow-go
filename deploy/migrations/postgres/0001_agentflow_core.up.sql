CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS agentflow_run_snapshots (
  run_id text PRIMARY KEY,
  version bigint NOT NULL,
  scenario_name text NOT NULL,
  status text NOT NULL,
  current_node_id text NOT NULL DEFAULT '',
  snapshot_json jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agentflow_run_snapshots_status_idx
ON agentflow_run_snapshots (status, updated_at DESC);

CREATE INDEX IF NOT EXISTS agentflow_run_snapshots_scenario_idx
ON agentflow_run_snapshots (scenario_name, updated_at DESC);

CREATE TABLE IF NOT EXISTS agentflow_jobs (
  id text PRIMARY KEY,
  type text NOT NULL,
  run_id text,
  payload_json jsonb NOT NULL,
  state text NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 1,
  last_error text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  available_at timestamptz NOT NULL,
  lease_worker_id text,
  lease_expires_at timestamptz
);

CREATE INDEX IF NOT EXISTS agentflow_jobs_lease_idx
ON agentflow_jobs (state, available_at, created_at, id);

CREATE INDEX IF NOT EXISTS agentflow_jobs_expired_lease_idx
ON agentflow_jobs (state, lease_expires_at)
WHERE lease_expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS agentflow_jobs_run_idx
ON agentflow_jobs (run_id)
WHERE run_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agentflow_runtime_events (
  id bigserial PRIMARY KEY,
  run_id text NOT NULL,
  sequence bigint NOT NULL,
  event_type text NOT NULL,
  scenario_name text NOT NULL DEFAULT '',
  trace_id text NOT NULL DEFAULT '',
  span_id text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,
  payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, sequence)
);

CREATE INDEX IF NOT EXISTS agentflow_runtime_events_run_sequence_idx
ON agentflow_runtime_events (run_id, sequence);

CREATE INDEX IF NOT EXISTS agentflow_runtime_events_run_updated_idx
ON agentflow_runtime_events (run_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS agentflow_runtime_events_type_time_idx
ON agentflow_runtime_events (event_type, occurred_at DESC);

CREATE TABLE IF NOT EXISTS agentflow_knowledge_embeddings (
  namespace text NOT NULL,
  document_id text NOT NULL,
  content text NOT NULL,
  metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  embedding vector(1536) NOT NULL,
  PRIMARY KEY (namespace, document_id)
);

CREATE INDEX IF NOT EXISTS agentflow_knowledge_embeddings_hnsw
ON agentflow_knowledge_embeddings
USING hnsw (embedding vector_cosine_ops);

CREATE INDEX IF NOT EXISTS agentflow_knowledge_embeddings_metadata_idx
ON agentflow_knowledge_embeddings
USING gin (metadata_json);