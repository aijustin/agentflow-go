# PostgreSQL Migrations

These SQL files define the default PostgreSQL schema expected by the production adapters:

- `agentflow.NewPostgresRunStateRepository`
- `agentflow.NewPostgresJobQueue`
- `agentflow.NewPostgresEventStore`
- `agentflow.NewPostgresVectorStore`

The migrations are plain SQL so application teams can import them into their preferred migration runner. The local Compose stack mounts this directory and executes `0001_agentflow_core.up.sql` during PostgreSQL initialization.

The vector table uses `vector(1536)`. Change the dimension in an application-owned migration if your embedding model uses a different size.

`NewPostgresEventStore` creates `agentflow_runtime_events` automatically by default so teams can enable the observability dashboard without running migrations first. These SQL files remain useful for locked-down production environments where schema changes must be reviewed, scheduled, or applied by a migration runner instead of application startup. Pass `SkipSchemaSetup: true` after the table has been created externally.