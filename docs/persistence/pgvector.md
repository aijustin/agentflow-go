# PostgreSQL pgvector 存储

pgvector 适配器使用 `database/sql` 实现 `knowledge.VectorStore`。应用提供驱动和连接池；AgentFlow 不强制引入 `pgx`、`lib/pq` 或任何其他驱动依赖。

## 构造函数

```go
store, err := agentflow.NewPostgresVectorStore(agentflow.PostgresVectorStoreConfig{
  DB:        db,
  TableName: "agentflow_knowledge_embeddings",
})
if err != nil {
  log.Fatal(err)
}
```

如果 `TableName` 为空，适配器使用 `agentflow_knowledge_embeddings`。也接受带 schema 的表名，例如 `knowledge.embeddings`。

## 表契约

按模型使用的 embedding 维度创建 `vector` extension 和表：

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

请使用 embedding profile 所需的维度。例如，`text-embedding-3-small` 通常使用 1536 维。

## 查询行为

适配器按 namespace 查询，并按 cosine distance 排序：

```sql
ORDER BY embedding <=> $query_vector::vector
```

得分以 `1 - distance` 返回，因此在 cosine search 中值越大越好。元数据以 JSONB 存储，并作为字符串元数据返回到每个结果上。`knowledge.Query.Filter` 会作为精确 JSONB containment filter 应用：`metadata_json @> $filter::jsonb`。

## 租户

`namespace` 是隔离 key。建议使用 `tenant-a/project-x/docs` 这样的租户/工作区/项目前缀。如果需要更强隔离，可结合 namespace 检查与独立数据库角色、schema 或行级安全。

## 运维注意事项

- 批量摄取可以用多个文档 embedding 调用 `Upsert`；每个文档会以 `ON CONFLICT` upsert 语义写入。
- 空向量和非有限向量值会在进入数据库前被拒绝。
- 适配器在 SQL 中应用 namespace filtering 和精确 metadata containment filtering。更高级的谓词可以在上层叠加，或通过未来查询选项增加。