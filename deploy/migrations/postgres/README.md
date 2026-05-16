# PostgreSQL Migrations

These SQL files define the default PostgreSQL schema expected by the production adapters:

- `agentflow.NewPostgresRunStateRepository`
- `agentflow.NewPostgresJobQueue`
- `agentflow.NewPostgresVectorStore`

The migrations are plain SQL so application teams can import them into their preferred migration runner. The local Compose stack mounts this directory and executes `0001_agentflow_core.up.sql` during PostgreSQL initialization.

The vector table uses `vector(1536)`. Change the dimension in an application-owned migration if your embedding model uses a different size.