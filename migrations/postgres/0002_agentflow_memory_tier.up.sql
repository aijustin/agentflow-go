CREATE TABLE IF NOT EXISTS agentflow_memory_tier_records (
  namespace_key text NOT NULL,
  record_id text NOT NULL,
  tier text NOT NULL,
  last_access_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  record_json jsonb NOT NULL,
  PRIMARY KEY (namespace_key, record_id)
);

CREATE INDEX IF NOT EXISTS agentflow_memory_tier_records_tier_idx
ON agentflow_memory_tier_records (namespace_key, tier, last_access_at DESC);
