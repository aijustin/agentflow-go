# PostgreSQL pgvector Store

The pgvector adapter implements `knowledge.VectorStore` with `database/sql`. Applications provide the driver and pool; AgentFlow does not force `pgx`, `lib/pq`, or any other driver dependency.

## Constructor

```go
store, err := agentflow.NewPostgresVectorStore(agentflow.PostgresVectorStoreConfig{
  DB:        db,
  TableName: "agentflow_knowledge_embeddings",
})
if err != nil {
  log.Fatal(err)
}
```

If `TableName` is empty, the adapter uses `agentflow_knowledge_embeddings`. Schema-qualified table names such as `knowledge.embeddings` are accepted.

## Table Contract

Create the `vector` extension and table with the embedding dimension used by your model:

```sql
CREATE EXTENSION IF NOT EXISTS vector;

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
```

Use the dimension required by your embedding profile. For example, `text-embedding-3-small` commonly uses 1536 dimensions.

## Query Behavior

The adapter queries by namespace and orders by cosine distance:

```sql
ORDER BY embedding <=> $query_vector::vector
```

Scores are returned as `1 - distance`, which makes larger values better for cosine search. Metadata is stored as JSONB and returned as string metadata on each result. `knowledge.Query.Filter` is applied as an exact JSONB containment filter with `metadata_json @> $filter::jsonb`.

## Tenancy

`namespace` is the isolation key. Use a tenant/workspace/project prefix such as `tenant-a/project-x/docs`. For stronger isolation, combine namespace checks with separate database roles, schemas, or row-level security.

## Operational Notes

- Batch ingestion can call `Upsert` with multiple document embeddings; each document is written with `ON CONFLICT` upsert semantics.
- Empty vectors and non-finite vector values are rejected before hitting the database.
- The adapter applies namespace filtering and exact metadata containment filtering in SQL. More advanced predicates can be layered above or added through future query options.