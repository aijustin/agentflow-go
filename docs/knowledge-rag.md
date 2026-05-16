# Knowledge and RAG

The RAG foundation has three public contracts:

- `llm.Embedder`: converts text into vectors.
- `knowledge.Loader`: loads source documents.
- `knowledge.Chunker`: splits source documents into citation-friendly chunks.
- `knowledge.VectorStore`: stores and queries embedded documents.
- `core.ToolExecutor`: exposes retrieval as a governed runtime tool.

The root package exposes production-ready wiring helpers for the common path:

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

The retriever tool accepts:

```json
{
  "query": "How do approval policies work?",
  "namespace": "tenant-a/docs",
  "limit": 5,
  "filter": {"source": "handbook"}
}
```

It returns a JSON payload with result IDs, content, scores, and metadata. The current pgvector adapter stores metadata and returns it with each hit, so callers can include document URLs, titles, versions, or citation IDs.

## Ingestion Pipeline

Use the filesystem loader, text splitter, and indexer to load documents into a vector store:

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

HTTP sources use the same loader contract:

```go
loader, err := agentflow.NewHTTPKnowledgeLoader(agentflow.HTTPKnowledgeLoaderConfig{
  URLs:      []string{"https://docs.example.test/handbook"},
  Namespace: "tenant-a/docs",
  Metadata:  map[string]string{"collection": "handbook"},
  MaxBytes:  2 << 20,
})
```

Chunk metadata includes `parent_id`, `chunk_index`, `chunk_count`, `chunk_start`, and `chunk_end`. The retriever response exposes these fields as a `citation` object so callers can render source references without parsing metadata manually.

## Scenario Shape

The runtime treats retrieval as a normal tool. Register the executor in Go and declare the tool in YAML:

```yaml
tools:
  knowledge.retrieve:
    type: knowledge.retriever
    description: Search approved knowledge collections for relevant context.
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

Agents use it through the standard autonomous tool loop. That means retrieval is covered by the same runtime audit, authorization, approval, rate-cap, timeout, retry, and redaction features as any other tool.

## Current Boundary

This slice provides provider capability helpers, embedding support for OpenAI-compatible APIs and mock tests, file and HTTP document loading, text chunking, batch indexing, the public vector store port, a pgvector baseline adapter, and a citation-aware retriever tool. More specialized ingestion connectors can implement `knowledge.Loader` without changing the indexing pipeline.