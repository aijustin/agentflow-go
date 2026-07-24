-- Transactional event outbox for the run-lifecycle event pipeline.
--
-- Writers (SaveWithEvents same-transaction saves and the observability
-- outbox sink) park events here with a per-run sequence minted under the
-- same advisory lock the event store uses; the framework relay then moves
-- unpublished rows into agentflow_runtime_events, whose
-- UNIQUE (run_id, sequence) constraint makes redelivery idempotent.
--
-- payload_json carries the FULL serialized core.Event envelope (not only
-- event.Payload) so the relay can reconstruct occurred_at, episode/session
-- correlation, and trace fields exactly as emitted.

CREATE TABLE IF NOT EXISTS agentflow_outbox (
  id bigserial PRIMARY KEY,
  run_id text NOT NULL,
  sequence bigint NOT NULL,
  event_type text NOT NULL,
  scenario_name text NOT NULL DEFAULT '',
  payload_json jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  published_at timestamptz
);

CREATE INDEX IF NOT EXISTS agentflow_outbox_unpublished_idx
ON agentflow_outbox (id) WHERE published_at IS NULL;

INSERT INTO agentflow_schema_migrations (version) VALUES (5)
ON CONFLICT (version) DO NOTHING;
