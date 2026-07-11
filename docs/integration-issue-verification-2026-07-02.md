# agentflow-go 集成问题核实说明（回复 agent-base 联调报告）

> **核实日期**：2026-07-02  
> **核实对象**：`agentflow-go-framework-issues-session-diagnostic.md`、`agentflow-go-integration-issues.md`  
> **框架版本**：`github.com/aijustin/agentflow-go` @ main（commit `ec16f837` 及之后）  
> **核实方式**：对照框架源码逐项验证，并在框架仓库落地已确认的真缺陷修复

---

## 1. 总体结论

两份报告**方向正确**（POS 会话诊断中的 token 膨胀、截断、记忆混乱等现象真实存在），但**问题归属划分有误**：

| 类别 | 数量 | 说明 |
|------|------|------|
| 框架真缺陷（已修复） | 3 | AF-012、AF-002、AF-008 |
| 框架真缺陷（当前不成立） | 1 | AF-001（nil map 写入已有保护） |
| 集成侧主因（agent-base 需修） | 2 | AF-011、AF-013 根因在 Profile/Policy 未传入或未启用 |
| 框架能力缺口（合理增强项） | 若干 | AF-003～AF-010、AF-014、AF-015 |
| 明确归属 agent-base | 若干 | hydrate 门控、HITL、凭证 UI、多 KB 只查第一个 namespace 等 |

**一句话**：30 万 token 膨胀 + 8192 硬截断是**真实现象**，但主因是 agent-base **未把 `ContextWindowTokens` 传给框架**且**未启用框架已有的 context/memory 治理策略**，不应全部归因于框架缺陷。

---

## 2. 已在框架侧修复（可升级验证）

### AF-012：`catalog.LoadSkillManifest` 丢失 `prompt_fragments`

**报告结论**：属实 ✅  
**修复**：`pkg/catalog/document.go` + `pkg/catalog/manifest.go`  
**根因**：`catalog.LoadSkillManifest` / `LoadToolManifest` 用 `yaml.Unmarshal` 直接反序列化到 `core.Skill` / `core.Tool`，这些结构体只有 `json` 标签。`yaml.v3` 不认 `json` 标签，导致 `prompt_fragments`、`compatible_agents`、`tool_policies`、`input_schema`、`side_effect` 等 snake_case 字段被静默丢弃。  
**注意**：`internal/adapter/config/yaml` 的 scenario 加载路径不受影响（有独立带 tag 结构）；仅 `pkg/catalog` 独立 manifest 路径有 bug。  
**验证**：`go test ./pkg/catalog/... -run 'SnakeCase|RoundTrip'`

### AF-002：OpenAI 兼容模型在正文输出 tool-call JSON

**报告结论**：属实 ✅  
**修复**：`internal/adapter/llm/openai/toolcall_normalize.go`，在 `ChatWithTools` 返回前调用 `normalizeContentToolCalls`  
**行为**：
- 仅当请求带工具且响应无结构化 `tool_calls` 时触发
- 支持单对象、数组、````json` 围栏
- 仅当 `name` 命中本次声明的工具名才提升（避免把普通 JSON 回答误判为工具调用）
**验证**：`go test ./internal/adapter/llm/openai/... -run ContentToolCall`

### AF-008：流式路径未覆盖 text tool-call 归一化

**报告结论**：部分属实 ✅（通过 AF-002 一并覆盖）  
**说明**：运行时对**带工具的 agent**，流式路径（`streamAnswer`）内部走 `answerWithTools` → `ChatWithTools`，不走裸 `StreamChat`。因此 AF-002 的归一化同时覆盖「流式 + 工具循环」场景。  
**仍存在的限制**：纯 `StreamChat`（无工具循环）路径仍不解析 tool-call；若 agent-base 有直接调用 `StreamChat` 做工具调用的路径，需单独评估。

---

## 3. 报告 P0 项核实：AF-011 / AF-013（主因不在框架）

### AF-011：`ContextPrepared` 硬截断且与 Profile 上下文窗口不一致

**报告结论**：现象属实，**根因判断不成立** ❌（主因在集成侧）

**框架实际行为**（`internal/application/runtime/runtime_context.go`）：

```go
policy := profile.Context
if policy.ContextWindowTokens == 0 {
    policy.ContextWindowTokens = profile.ContextWindowTokens
}
if policy.ReservedOutputTokens == 0 {
    policy.ReservedOutputTokens = profile.MaxOutputTokens
}
result := e.contextManager(ctx, runID, agent, policy).Prepare(raw)
```

**推导公式**（`pkg/contextwindow/policy.go`）：

```go
if p.ContextWindowTokens > 0 && p.MaxInputTokens == 0 {
    p.MaxInputTokens = p.ContextWindowTokens - p.ReservedOutputTokens
}
if p.MaxInputTokens <= 0 {
    p.MaxInputTokens = 8192  // 仅当上述均为 0 时的兜底
}
```

**诊断中 `max_input_tokens: 8192` 的含义**：框架收到的 `LLMProfileRef` 中 **`ContextWindowTokens == 0` 且 `Context.MaxInputTokens == 0`**。agent-base 在 `config.local.yaml` / `cmd/server/main.go` 设置的 `chatProfile.ContextWindowTokens = 900000` **没有写入传给框架的 Profile**。

**agent-base 需检查**：
1. 构建 `core.LLMProfileRef` 时是否赋值 `ContextWindowTokens`
2. 是否通过 scenario yaml 或 `Framework` 选项正确注册 LLM profile
3. 如需摘要策略，设置 `profile.Context.Strategy = sliding_window_with_summary`（默认 `none` 在超预算时会 sliding trim 但不摘要，这是设计行为，见 `pkg/contextwindow/manager.go:80-93`）

**框架 yaml 配置路径已支持**（`internal/adapter/config/yaml/config.go:40`、`config_test.go:140`）：

```yaml
llms:
  default:
    context_window_tokens: 128000
    max_output_tokens: 4096
    context:
      strategy: sliding_window_with_summary
      tool_result_max_tokens: 400
      compression:
        enabled: true
```

---

### AF-013：Session memory 全量持久化 tool 输出导致上下文爆炸

**报告结论**：现象属实，**「框架无上限/transform」不成立** ❌（能力已存在，未启用）

**框架已有机制**（均未在 agent-base 集成中启用）：

| 机制 | 位置 | 作用 |
|------|------|------|
| `ToolResultMaxTokens` + `compactToolResultForContext` | `runtime_context.go:13-66` | LLM 调用前压缩 tool 结果 |
| `Compression` + `compressToolMessages` | `contextwindow/manager.go:280` | 超 trigger ratio 时压缩 tool 消息 |
| `RoleBudgets.Tool` | `contextwindow/manager.go:208-228` | 按 role 限制 token 预算 |
| `StaleToolTurns` + `evictStaleToolMessages` | `runtime_context_governance.go:179` | 淘汰旧 tool turn |
| `MemoryRecallLimit` | `runtime_memory.go:160-164` | 限制 recall 条数 |
| Memory tier (hot/warm/cold + LLM summary) | `pkg/memory/tier/` | 分层记忆与冷层摘要 |

**Memory 写入路径**（`runtime_memory.go:43-49`）确实存完整 `ToolResult` JSON——这是正常行为；**治理应在 context policy 层**，而非禁止 memory 存原文。

**agent-base 建议**（双保险可保留）：
1. **优先**：在传给框架的 Profile 上配置 `Context` policy（见 AF-011）
2. **可选保留**：`trace_executor.go` 出口 cap 作为 integrator 层双保险

---

## 4. 其他 AF 条目快速核实

| ID | 报告 | 核实 | 说明 |
|----|------|------|------|
| AF-001 | RunSnapshot nil map panic | **当前不成立** | Load 后 map 可能为 nil，但所有写入点有 `if == nil { make(...) }` 保护（`runtime_helpers.go`、`workflow.go` 等） |
| AF-003 | 多 KB namespace 检索 | **框架能力缺口** | 框架只支持单 namespace 查询；多 collection 各注册独立 retriever tool。报告中的「只查第一个」bug 在 agent-base `manager.go` |
| AF-004 | 流式 Run 与 EventHub 双通道 | **合理增强** | 框架设计如此；integrator 需 merge，属 DX 优化 |
| AF-005 | 事件类型不统一 | **合理增强** | 框架有 `EventToolCalled` 等；UI 层事件可由 integrator 包装 |
| AF-006 | Postgres schema 混部 | **已规避** | agent-base 用 `SkipSchemaSetup` + 自有 migration |
| AF-007 | Prompt/Script Skill 路径分裂 | **合理增强** | 架构设计差异，非 bug |
| AF-009 | Embedding 维度未校验 | **合理增强** | 启动校验可后续加 |
| AF-010 | 缺少 ProductionHTTPHandler | **规划中** | README 已标注 |
| AF-014 | MemoryRead 缺 provenance | **合理增强** | 框架 `MemoryRead` 只报框架视角条数；integrator 字段如 `memory_hydrated_from_chat` 是 agent-base 注入 |
| AF-015 | 截断未优先保留 user | **部分属实** | 默认 sliding window 按新旧截断；可用 `RoleBudgets.User` 配置保留策略 |

---

## 5. 用户可见现象 → 根因对照（修正版）

| 用户可见现象 | 报告归因 | 修正后主因 |
|--------------|----------|------------|
| 30 万 token / 频繁截断 | AF-011 + AF-013 | **集成侧 Profile 未传入 900000** + **Context policy 未启用** + tool 大 JSON 正常进 memory |
| `max_input_tokens` 固定 8192 | AF-011 框架 bug | **集成侧 Profile 双零 → 框架兜底 8192** |
| 「第一次 POS 打印问题」（记忆混乱） | AF-015 + hydrate | **截断丢早期 user**（截断因 8192）+ **agent-base hydrate 门控** |
| 「47 个 POS 工作站」被质疑 | 平台 prompt | 数据真实；陈述规范属 agent-base |
| 凭证过期 vs 尚未登录 | 平台 prompt + UI drift | agent-base |
| Prompt Skill 不生效 | AF-012 | **框架 bug，已修复** |
| Qwen 不调工具 | AF-002 | **框架 bug，已修复** |
| 多 KB 只检索第一个 | AF-003 | **agent-base `manager.go` bug**；框架无原生 multi-namespace API |

---

## 6. 分工与下一步

### agentflow-go（框架）— 已完成

- [x] AF-012：catalog manifest snake_case 字段加载
- [x] AF-002 / AF-008：OpenAI 兼容网关正文 tool-call 归一化（含 `StreamChatWithTools`）
- [x] AF-014：MemoryRead 增加 `messages_by_provenance` / `messages_by_role` / `stored_messages`；hydrate 契约见 `docs/session-memory-hydrate.md`
- [x] AF-015：ContextPrepared 增加 `dropped_user_messages` + 默认 pin user + `ContextIncomplete` 事件
- [x] AF-003 / AF-019：`knowledge.NewMultiNamespaceRetriever`（global_rank / balanced，metadata `namespace`/`kb_id`）
- [x] AF-004：`Framework.StreamRun` 统一 `StreamFrame`（token/event/done）
- [x] AF-005：`EventCategory` / `DisplayLabel` / `EventFilterPresetProductUI`
- [x] AF-007：`SkillKind` + `EventSkillApplied` + `pkg/skill.ScriptRuntime` 接口
- [x] AF-009：Postgres `WithExpectedDimension` + `ReindexRequiredError`；Indexer 向量长度一致性校验
- [x] AF-010：`NewProductionHTTPHandler` 已提供（文档状态更正）
- [x] AF-011：`policy_source` / `fallback_applied` 可观测
- [x] AF-013：MemoryWrite `message_bytes`/`tier`/`tool_name`/`transformed`；`ToolOutputMaxBytes`
- [x] AF-016：`RunRequest.TrustMode=full_trust` 跳过 tool approval pause
- [x] AF-020：`ToolOutputTransform` + JSON-aware truncate（`WithToolOutputTransform`）
- [x] AF-021：Stale 窗口默认排除 denied/empty；`stale_dropped_tool_turns` 等字段

### agent-base（平台）— 建议优先

1. **P0 — Profile 接线**：确认 `ContextWindowTokens` 写入 `core.LLMProfileRef` 并注册到 Framework
2. **P0 — Context Policy**：配置 `strategy`、`tool_result_max_tokens`、`stale_tool_turns`、`compression.enabled` 等
3. **P0 — TrustMode**：full_trust 会话传 `RunRequest.TrustMode = "full_trust"`
4. **P1 — hydrate 门控**：指纹同步（见 `docs/session-memory-hydrate.md`）
5. **P1 — 多 KB**：可改用框架 `NewMultiNamespaceRetriever` 或保留平台 orchestrator
6. **P2 — 注册** `WithToolOutputTransform("knowledge_retrieve", CompactRetrieveResponseForLLM)`

### 联调验收建议

升级框架到含上述修复的版本后，在 agent-base 侧：

1. 确认 `ContextPrepared.max_input_tokens` ≈ `ContextWindowTokens - max_output_tokens`（非 8192）；检查 `policy_source`
2. Replay `c9aa6e2f`：budget denial 后 LLM context 仍含最近 successful retrieve（AF-021）
3. 大 JSON tool result + `tool_result_max_tokens`：LLM 侧仍为可解析 JSON（AF-020）
4. Prompt Skill / Qwen tool-call / StreamRun 事件合并

---

## 7. 参考代码索引

| 主题 | 文件 |
|------|------|
| Profile → Context Policy 继承 | `internal/application/runtime/runtime_context.go` |
| 8192 兜底 + policy_source | `pkg/contextwindow/policy.go` |
| JSON-aware truncate | `pkg/contextwindow/transform.go` |
| Stale 分类 | `internal/application/runtime/runtime_context_governance.go` |
| TrustMode | `internal/application/runtime/runtime.go` / `runtime_continue.go` |
| StreamRun | `framework_stream.go` |
| MultiNamespaceRetriever | `pkg/knowledge/multi_namespace.go` |
| Hydrate 契约 | `docs/session-memory-hydrate.md` |

---

## 8. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-07-02 | 初版：逐条核实 AF-001～AF-015，标注框架已修复项与 agent-base 待办 |
| 2026-07-11 | 全量落地 AF-003/004/005/007/008/009/011/013/016/019/020/021；更正 AF-010 状态 |
