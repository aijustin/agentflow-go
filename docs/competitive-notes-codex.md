# Codex 对照笔记（只吸收库级优秀点）

对照对象：OpenAI Codex（`codex-rs/core`，编码 Agent 产品）与本库（可嵌入 Go Agent 运行时）。

投资边界见 [orchestration-modes.md](./orchestration-modes.md)：默认深度 = **autonomous**；不搬沙箱/壳工具/多 Agent 产品面。

---

## 已对齐（不必再搬）

| Codex | agentflow-go |
|-------|--------------|
| 并行工具 + 串行门闸 | `runtime_tool_batch` + `tool_path_lock` |
| 中途改口 | `Framework.Interject` |
| 工具/答案审批暂停 | HITL + `tool.approval: pause` |
| 上下文压缩 | `pkg/contextwindow` |
| 重复工具节流 | `runtime.doom_loop_limit` |
| 完成契约 | `Agent.CompletionRequirement` |

---

## 已吸收（本轮反哺）

| Codex 模式 | 本库落地 | 包 / 入口 |
|------------|----------|-----------|
| Sampling `StepContext` 冻结广告工具集 | 每轮 sample 冻结 `AdvertisedTools`，dispatch 仅允许集合内 | `pkg/toolorch.SamplingStepContext` |
| Steer drain 策略（含 post-compact） | `InterjectDrainPolicy`：before_sample / after_tool_batch / defer_until_post_compact | `pkg/interjection` + runtime |
| Approval cache + orchestrator（无 sandbox） | `ApprovalStore` + `ToolOrchestrator`；沙箱 escalate 由宿主 `AttemptResult` | `pkg/toolorch` |
| Compact 注入位契约 | `InsertBeforeLastUserMessage`（reminder / initial context） | `pkg/contextwindow` |
| Stop-hooks | 可选 `TurnStopHook`：可否决结束并注入续跑 | `pkg/core.TurnStopHook` |
| Guardian 拒绝熔断（精简） | HITL deny 计数熔断（无 LLM reviewer） | `runtime.hitl_deny_limit` + `pkg/toolorch.DenyBreaker` |

---

## 明确不吸收（宿主 / 产品层）

| Codex | 原因 |
|-------|------|
| OS sandbox / unified_exec / apply-patch | ToolExecutor 边界，宿主实现 |
| Code-mode 嵌套 JS cell | 产品特异 |
| Multi-agent control + mailbox 相位机 | 产品面；库侧 hybrid/并行已够 |
| WorldState 全套差分领域模型 | 只借鉴「typed sections」思想 |
| Memories 异步 Phase1/2 | 宿主 background job |
| Collaboration Plan UI | 与未接线 `pkg/planmode` 另议 |
| 传输层 WS→HTTPS fallback | LLM Gateway 适配器职责 |

---

## 优先级回顾

```text
P0  StepContext 冻结 + Interject drain 策略
P1  ApprovalStore / ToolOrchestrator + compact 注入位
P2  TurnStopHook + HITL deny 熔断
```
