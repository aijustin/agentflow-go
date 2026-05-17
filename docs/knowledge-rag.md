# 知识库与 RAG

RAG 基础由几个公共契约组成：

- `llm.Embedder`：将文本转换为向量。
- `knowledge.Loader`：加载源文档。
- `knowledge.Chunker`：将源文档拆分为便于引用的分块。
- `knowledge.VectorStore`：存储和查询已向量化的文档。
- `core.ToolExecutor`：将检索能力作为受治理的运行时工具暴露。

根包为常见路径提供生产可用的装配辅助函数：

```go
provider := agentflow.NewOpenAICompatibleProvider([]llm.Profile{
  {
    Name:         "chat",
    Provider:     "openai-compatible",
    Model:        "qwen/qwen3.6-35b-a3b",
    Endpoint:     os.Getenv("OPENAI_COMPATIBLE_BASE_URL"),
    APIKeyEnv:    "OPENAI_API_KEY",
  },
  {
    Name:         "embed",
    Provider:     "openai-compatible",
    Model:        "text-embedding-3-small",
    Endpoint:     os.Getenv("OPENAI_COMPATIBLE_BASE_URL"),
    APIKeyEnv:    "OPENAI_API_KEY",
    Capabilities: []llm.Capability{llm.CapEmbed},
  },
}, nil)

store, err := agentflow.NewPostgresVectorStore(agentflow.PostgresVectorStoreConfig{
  DB:        db,
  TableName: "agentflow_knowledge_embeddings",
})
if err != nil {
  log.Fatal(err)
}

retriever, err := agentflow.NewRetrieverTool(agentflow.RetrieverToolConfig{
  Embedder:     provider,
  Store:        store,
  Profile:      "embed",
  Namespace:    "tenant-a/docs",
  DefaultLimit: 5,
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithLLMGateway(provider),
  agentflow.WithToolExecutor("knowledge.retrieve", retriever),
)
```

检索工具接受：

```json
{
  "query": "审批策略如何工作？",
  "namespace": "tenant-a/docs",
  "limit": 5,
  "mode": "hybrid",
  "filter": {"source": "handbook"}
}
```

它返回包含结果 ID、内容、得分和元数据的 JSON 载荷。当前 pgvector 适配器会存储元数据，并在每个命中结果中返回它，因此调用方可以包含文档 URL、标题、版本或引用 ID。

## 混合检索与重排

`pkg/knowledge` 现在定义了两个可选扩展端口：

- `HybridSearcher`：当向量存储同时支持关键词/全文检索和向量相似度时，实现 `HybridQuery`。
- `Reranker`：在候选结果返回后，根据原始 query 对结果重新排序。

检索工具会在 `mode: hybrid` 且底层 store 实现 `HybridSearcher` 时调用混合检索；否则自动回退到普通向量查询。配置了 `Reranker` 时，工具会先扩大候选集，再重排并截断到用户请求的 `limit`：

```go
retriever, err := agentflow.NewRetrieverTool(agentflow.RetrieverToolConfig{
  Embedder:            provider,
  Store:               store,
  Profile:             "embed",
  Namespace:           "tenant-a/docs",
  DefaultLimit:        5,
  SearchMode:          knowledge.SearchModeHybrid,
  CandidateMultiplier: 3,
  Reranker:            myReranker,
})
```

这让 pgvector 基线保持简单，同时允许企业应用接入 PostgreSQL full-text、Elasticsearch/OpenSearch、专用 reranker 服务或内部知识平台。

## 摄取流水线

使用文件系统加载器、文本切分器和索引器将文档加载到向量存储：

```go
loader, err := agentflow.NewFileKnowledgeLoader(agentflow.FileKnowledgeLoaderConfig{
  Paths:     []string{"./docs"},
  Namespace: "tenant-a/docs",
  Metadata:  map[string]string{"collection": "handbook"},
  MaxBytes:  2 << 20,
})
if err != nil {
  log.Fatal(err)
}

docs, err := loader.Load(ctx)
if err != nil {
  log.Fatal(err)
}

splitter, err := knowledge.NewTextSplitter(knowledge.TextSplitterConfig{
  MaxRunes:     1200,
  OverlapRunes: 120,
})
if err != nil {
  log.Fatal(err)
}

indexer, err := agentflow.NewKnowledgeIndexer(agentflow.KnowledgeIndexerConfig{
  Embedder:  provider,
  Store:     store,
  Profile:   "embed",
  Namespace: "tenant-a/docs",
  BatchSize: 32,
  Chunker:   splitter,
})
if err != nil {
  log.Fatal(err)
}

result, err := indexer.Index(ctx, docs)
if err != nil {
  log.Fatal(err)
}
fmt.Printf("indexed %d documents into %d chunks\n", result.Documents, result.Chunks)
```

HTTP 来源使用同一个加载器契约：

```go
loader, err := agentflow.NewHTTPKnowledgeLoader(agentflow.HTTPKnowledgeLoaderConfig{
  URLs:      []string{"https://docs.example.test/handbook"},
  Namespace: "tenant-a/docs",
  Metadata:  map[string]string{"collection": "handbook"},
  MaxBytes:  2 << 20,
})
```

分块元数据包含 `parent_id`、`chunk_index`、`chunk_count`、`chunk_start` 和 `chunk_end`。检索响应会将这些字段作为 `citation` 对象暴露，因此调用方可以渲染来源引用，而不需要手动解析元数据。

## 场景形态

运行时将检索视为普通工具。在 Go 中注册执行器，并在 YAML 中声明工具：

```yaml
tools:
  knowledge.retrieve:
    type: knowledge.retriever
    description: 搜索已获批知识集合中的相关上下文。
    side_effect: read
    approval: never
    input_schema:
      type: object
      required: [query]
      properties:
        query:
          type: string
        limit:
          type: integer
```

Agent 通过标准自主工具循环使用它。这意味着检索与其他工具一样，会受到运行时审计、授权、审批、速率限制、超时、重试和脱敏能力覆盖。

## 当前边界

这一切片提供 Provider 能力辅助函数、OpenAI-compatible API 和 mock test 的 embedding 支持、文件/HTTP 文档加载、文本分块、批量索引、公共向量存储端口、pgvector 基线适配器、混合检索扩展端口、重排扩展端口，以及支持引用的检索工具。更专用的摄取连接器可以实现 `knowledge.Loader`，无需改变索引流水线。