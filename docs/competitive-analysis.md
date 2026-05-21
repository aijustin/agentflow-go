# 竞品调研与差距关闭计划

本文档对比 agentflow-go 与 LangGraph、LlamaIndex Workflows、CrewAI Flows、Haystack 2 在 **流程编排、RAG、上下文治理** 三个维度的差异，并跟踪本仓库的关闭进度。

## 能力矩阵

| 维度 | agentflow-go | LangGraph | LlamaIndex | CrewAI | Haystack 2 |
|------|-------------|-----------|------------|--------|------------|
| 编排模型 | YAML 三模式 | 代码状态图 | 事件 `@step` | Flow + Crew | Pipeline 多图 |
| Agentic RAG | CRAG/Self-RAG 模板 + router 节点 | Adaptive/Corrective/Self-RAG 图 | Critic-reflect | MCP 外部 | Agentic pipeline |
| Hybrid 检索 | pgvector + FTS RRF | 生态 | 原生 | 外部 | 多 retriever |
| 认知记忆 | `pkg/memory/cognitive` | Store | Context KV | Cognitive Memory | state_schema |
| 上下文治理 | 规则 + LLM 摘要 + stale 淘汰 | LangSmith | Postprocessor | LLM 编码 | 组件级 |

## 关闭进度

| 能力 | 状态 | 入口 |
|------|------|------|
| LLM 摘要压缩 | ✅ | `runtime_context.go` + `summary_mode: llm` |
| YAML 知识集合绑定 | ✅ | `scenario.knowledge.collections` + `WithKnowledgeRegistry` |
| Hybrid 检索 (FTS+向量 RRF) | ✅ | `postgres.Store.HybridQuery` |
| 内置 Reranker | ✅ | `NewScoreReranker` / `NewLLMReranker` |
| Corrective RAG 模板 | ✅ | `examples/corrective_rag.yaml` |
| Adaptive RAG / Query Router | ✅ | `query_router` workflow 节点 |
| Planning 闭环 | ✅ | `planning.execute` + replan + tool 引导 |
| 分层记忆 | ✅ | `memory_recall_limit` + `long_term` scope |
| Tool schema 裁剪 | ✅ | `context.tool_schema_pruning` |
| Stale tool 结果淘汰 | ✅ | `context.stale_tool_turns` |
| 认知记忆端口 | ✅ | `pkg/memory/cognitive` |
| MCP scenario 配置 | ✅ | `scenario.mcp.servers` + `WireMCPTools` |
| Supervisor 多 agent | ✅ | `supervisor` workflow 节点 |
| Self-RAG 模板 | ✅ | `examples/self_rag.yaml` |
| 租户检索隔离 | ✅ | retriever namespace 前缀 tenant |

## 参考

- [orchestration-flow.md](./orchestration-flow.md)
- [knowledge-rag.md](./knowledge-rag.md)
- [enterprise-roadmap.md](./enterprise-roadmap.md) M5
