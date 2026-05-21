# PostgreSQL migrations

Versioned SQL for adapters that store data in PostgreSQL:

- run snapshots (`agentflow_run_snapshots`)
- async job queue (`agentflow_jobs`)
- runtime events (`agentflow_runtime_events`)
- knowledge embeddings (`agentflow_knowledge_embeddings`)

Apply these files with your own migration runner. The vector table uses `vector(1536)`; change the dimension if your embedding model differs.

`NewPostgresEventStore` can create `agentflow_runtime_events` at startup for local development. In locked-down environments, apply `0001_agentflow_core.up.sql` first and pass `SkipSchemaSetup: true`.
