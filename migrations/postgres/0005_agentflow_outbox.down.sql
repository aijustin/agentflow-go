DELETE FROM agentflow_schema_migrations WHERE version = 5;

DROP INDEX IF EXISTS agentflow_outbox_unpublished_idx;
DROP TABLE IF EXISTS agentflow_outbox;
