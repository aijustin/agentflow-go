# 竞品调研与差距关闭计划

本文档对比 agentflow-go 与 LangGraph、LlamaIndex Workflows、CrewAI Flows、Haystack 2 在 **流程编排、RAG、上下文治理** 三个维度的差异，并跟踪本仓库的关闭进度。

**LangGraph 深度对比**（架构、编排对齐、Side-by-side）：见 [competitive-analysis-langgraph.md](./competitive-analysis-langgraph.md)。  
**编排对齐路线图**（subgraph / map / checkpoint）：见 [orchestration-parity.md](./orchestration-parity.md)。

## 能力矩阵

| 维度 | agentflow-go | LangGraph | LlamaIndex | CrewAI | Haystack 2 |
|------|-------------|-----------|------------|--------|------------|
| 编排模型 | Go builder 三模式（+ legacy YAML 导出） | 代码状态图 | 事件 `@step` | Flow + Crew | Pipeline 多图 |
| Agentic RAG | CRAG/Self-RAG 模板 + router 节点 | Adaptive/Corrective/Self-RAG 图 | Critic-reflect | MCP 外部 | Agentic pipeline |
| Hybrid 检索 | pgvector + FTS RRF | 生态 | 原生 | 外部 | 多 retriever |
| 认知记忆 | `memory.CognitiveMemory`（`pkg/memory/cognitive.go`） | Store | Context KV | Cognitive Memory | state_schema |
| 上下文治理 | 规则 + LLM 摘要 + stale 淘汰 | LangSmith | Postprocessor | LLM 编码 | 组件级 |

## 关闭进度

| 能力 | 状态 | 入口 |
|------|------|------|
| LLM 摘要压缩 | ✅ | `runtime_context.go` + `summary_mode: llm` |
| YAML 知识集合绑定 | ✅ | `scenario.knowledge.collections` + `WithKnowledgeRegistry` |
| Hybrid 检索 (FTS+向量 RRF) | ✅ | `postgres.Store.HybridQuery` |
| 内置 Reranker | ✅ | `NewScoreReranker` / `NewLLMReranker` |
| Corrective RAG 模板 | ✅ | `builder.CorrectiveRAG("assistant")` |
| Adaptive RAG / Query Router | ✅ | `query_router` workflow 节点 |
| Planning 闭环 | ✅ | `planning.execute` + replan + tool 引导 |
| 分层记忆 | ✅ | `pkg/memory/tier` + runtime/Framework 接线；见 [memory-tier 计划](superpowers/plans/2026-05-21-memory-tier.md) |
| Tool schema 裁剪 | ✅ | `context.tool_schema_pruning` |
| Stale tool 结果淘汰 | ✅ | `context.stale_tool_turns` |
| 认知记忆端口 | ✅ | `CognitiveMemory` + tier `DualWriteManager` / `CognitiveAdapter`；Framework `WithCognitiveMemory` + tier 自动索引 |
| MCP scenario 配置 | ✅ | `scenario.mcp.servers` + `WireMCPTools` |
| Supervisor 多 agent | ✅ | `supervisor` workflow 节点 |
| Self-RAG 模板 | ✅ | `builder.SelfRAG("assistant")` |
| 租户检索隔离 | ✅ | retriever namespace 前缀 tenant |

## 实现说明

- **分层记忆**：`pkg/memory/tier` 提供 hot/warm/cold 契约、Manager、CompositeStore 与 runtime/Framework 接线；详见 [memory-tier 计划](superpowers/plans/2026-05-21-memory-tier.md)。
- **认知记忆**：tier 路径通过 `DualWriteManager` 双写 cognitive 索引，`CognitiveAdapter` 暴露 `CognitiveMemory` 端口；Framework 在 `tiers.enabled` 时自动挂载 in-memory cognitive 索引，亦可通过 `WithCognitiveMemory` 注入。

## 参考

- [competitive-analysis-langgraph.md](./competitive-analysis-langgraph.md)
- [orchestration-flow.md](./orchestration-flow.md)
- [knowledge-rag.md](./knowledge-rag.md)
- [enterprise-roadmap.md](./enterprise-roadmap.md) M5
