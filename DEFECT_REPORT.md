# agent-framework 主流程缺陷分析报告

- **项目**：agentflow-go（Go ≈99%，约 41k 行）
- **分析范围**：agent 主流程（Framework 入口 → Engine 消息循环 → LLM/工具调度 → 状态持久化 → checkpoint/resume → lease/async）
- **主调用链**：
  `Framework.Run/Stream` → `Engine.Run/Stream` → `answerForAgent` → `answerWithToolsFrom` → `dispatchToolCalls` → `executeToolBatch` → `dispatchToolWithOptions`
- **日期**：2026-08-06

---

## 摘要

| 级别 | 编号 | 问题 |
|---|---|---|
| 🔴 高 | D1 | 治理计数器 check-then-act 竞态，并行批处理可绕过 rate cap / doom loop / governance（**2026-08-06：主体已过时；governance 计数与 workflow 计数器两处残留已修复**） |
| 🔴 高 | D2 | 失败/取消终态持久化无 CAS 重试，run 可能永久滞留 Running（**2026-08-06：经核实已修复，已补回归测试**） |
| 🟠 中 | D3 | 用 `fmt.Sprintf("%q")` 构造 JSON，可能产出非法 JSON（**2026-08-07：已修复，全部改 json.Marshal，见节内标注**） |
| 🟠 中 | D4 | HITL 批准恢复路径未应用 `ToolOutputMaxBytes` 截断（**2026-08-07：经核实重构后截断语义已具备；本次统一为批路径 materialize 函数并补回归测试**） |
| 🟠 中 | D5 | 审批暂停-恢复后，该轮 assistant + tool results 可能漏写 conversation memory（**2026-08-07：经核实已被重构修复，现有回归测试覆盖，复核通过**） |
| 🟠 中 | D6 | detached stream 取消观察者每 100ms 全量 `LoadAuthorized`，DB 压力大 |
| 🟠 中 | D7 | checkpoint 写入与 `gate.Pause` 之间崩溃会留下 Running + 悬挂 checkpoint，且恢复不校验 gate token（**2026-08-07：已修复，pending_pause 标记 + fail-closed 守卫 + reaper 清理 + `ContinueRunWithToken`，见节内标注**） |
| 🟡 低 | D8 | `EventRunCompleted` 在 final 输出外置后携带 nil payload |
| 🟡 低 | D9 | `ensureRunPaused` 单次 CAS 无重试，gate 已发 token 但状态仍可能 Running |
| 🟡 低 | D10 | `toolSpecs :=` 循环内遮蔽外层参数，replan 可能用旧 specs |
| 🟡 低 | D11 | `Framework.Stream` 返回的流不消费也不 cancel 时，goroutine/lease 永久挂起 |
| 🟡 低 | D12 | ctx 取消早退时 `orchestrator.AfterAttempt` 可能被跳过 |
| 🟡 低 | D13 | `persistRunCompleted` 重试全失败时留下孤儿 blob |

**修复优先级建议**：先修 D1、D2（正确性/一致性核心），再做 D3 的全局 `json.Marshal` 替换，随后按 D4→D5→D7→D9 的顺序补齐 checkpoint 相关路径，其余按排期处理。

---

## 🔴 高危

### D1 治理计数器 check-then-act 竞态（rate cap / doom loop / governance 可被绕过）

> **状态（2026-08-06 复核）：报告主体论断已过时；两处残留竞态已修复。**
>
> - **已过时的部分**：`toolCallTracker.reserveToolCall`（`internal/application/runtime/tool_call_tracker.go:77`）已是单锁内"判定 + 预留"（committed + 在途 reserved 一起计数，两阶段 reserve/commit/release），phase-2 Inspector 重构（`inspectors.go:170` `execution_budget`）保留了该语义。N 个并发同名调用 + RateCap=k 恰好放行 k 个，报告的"先读后写无锁"不再成立。
> - **残留①（已修复）**：governance 判定用的计数（`CallCount`/`SameInputCalls`/`TotalCalls`）此前是 committed-only（`inspectGovernance` 旧代码读 `tracker.totalSuccesses()`），并发批内 N 个调用各自观察到相同的批前计数，可集体越过 `governance.NewToolBudgetPolicy` 之类的预算。修复：`reserveToolCall` 在同一把锁内返回含在途预留的 `toolCallCounts` 视图（`tool_call_tracker.go:104` `countsLocked`），`inspectExecutionBudget`/`inspectGovernance`（`inspectors.go:170/191`）与 delegation 分支（`runtime_tools.go`）改用该视图；串行语义不变（无在途时等于 committed）。
> - **残留②（已修复）**：workflow 编排层有**独立于 tracker 的持久化计数器**（snapshot 变量 `workflow_tool_call_counts`），旧实现 `toolCallCount`（读）→ 执行 → `incrementToolCallCount`（写）是教科书式 check-then-act，并行兄弟节点（`MaxParallel>1`）可双双越过 `RateCap`。修复：`reserveWorkflowToolCall`（`internal/application/orchestration/workflow_tool_approval.go:136`）把"检查 + 预留"折叠进一次 `saveSnapshotWithRetry` CAS；执行失败由 `releaseWorkflowToolCall`（同文件 L173，best-effort，泄漏方向安全）归还，保持"仅成功执行计数"语义。
> - **回归测试**：`tool_call_tracker_test.go` `TestToolCallTrackerConcurrentReservations`（表驱动 rate-cap / doom-loop 变体）与 `TestToolCallTrackerCountsIncludeInFlightSiblings`；`runtime_tool_batch_test.go` `TestExecuteToolBatchEnforcesBudgetsUnderConcurrency`（表驱动 rate-cap / doom-loop / governance 三变体，断言恰好执行 k 次，governance 变体修复前红：6 并发执行 6 次 > cap 2）；`workflow_tool_approval_test.go` `TestWorkflowRunnerEnforcesRateCapAcrossParallelNodes`（修复前红：两并行节点都执行）与 `TestWorkflowRunnerRateCapReservationReleasedOnFailure`。均经 `-race -count=3` 验证。

- **位置**：`internal/application/runtime/runtime_tools.go` `dispatchToolWithOptions`（约 L65–L180）；`internal/application/runtime/runtime_tool_batch.go` `executeToolBatch`
- **现象**：
  - `dispatchToolWithOptions` 在**执行工具前**读取 `tracker.nameCount(call.Name)` / `sameInputCount` 判断 `DoomLoopLimit`、`RateCap`、governance 预算，通过后才真正执行，**执行完成后**才 `recordAttempt`。
  - `executeToolBatch` 用 errgroup + semaphore **并发派发**同一批工具调用，多个 goroutine 同时对同名工具做检查。
- **后果**：假设 `RateCap=3`，4 个并发调用可同时读到计数 `<3` 全部放行，实际执行 4 次；doom loop / governance 同理被绕过。治理限制在高并发批处理下**不可靠**。
- **修复方向**：
  1. 在 `toolCallTracker` 上提供原子 `checkAndReserve(name, fingerprint) (ok bool)`：同一把锁内完成"检查 + 递增"，返回失败时生成标准化的限流 `ToolResult`。
  2. 执行失败/被审批拦截时调用 `rollback` 归还配额（视产品语义决定是否归还）。
  3. 补一个并发单测：N 个同名并发调用 + `RateCap=k`，断言恰好执行 k 次。

### D2 失败/取消终态持久化与成功路径不对称（run 滞留 Running）

> **状态（2026-08-06 复核）：报告论断已过时，当前代码已满足修复方向；补充了兜底权衡注释与回归测试。**
>
> - `markRunFailedMode`（`internal/application/runtime/runtime_helpers.go:807`）与 `markRunCancelled`（同文件 L865）**当前都走 `saveSnapshotWithRetry`**（5 次 jitter 退避的 CAS 重试），与 `persistRunCompleted`（L711）对称；报告所述"单次 `saveRunSnapshot` 撞 `ErrStaleSnapshot` 就放弃"已不存在。
> - 语义约束逐项核实：①Paused / 带 tool_approval checkpoint 且非 force 仍跳过（mutate 内 `return nil`，L817 附近）；②已是 Cancelled 的补发 `EventRunCancelled` 逻辑保留（L852）；③`ErrStaleFence` 在 `saveSnapshotWithRetry`（L595 附近）只认 `ErrStaleSnapshot` 重试，fence 错误直接穿透；④重试耗尽的兜底选择了**不强制覆盖写、仅告警**：能连续赢 5 次 jitter CAS 的写者正在活跃推进该 run（step output / pause / cancel），盲目终态写可能覆盖合法的 Paused/Cancelled——比滞留 Running 更糟（滞留可被 reaper / `RetryFailedRun` 恢复，被覆盖的 Paused run 其审批 token 作废）。`markRunAbandoned` 位于 Framework 层且需要 fence token，不适合 engine 层直接复用。权衡已写入 `markRunFailedMode` 的注释。
> - **回归测试**：`runtime_helpers_unit_test.go` `TestEngineTerminalStatusSurvivesStaleSnapshotConflict`（表驱动 failed/cancelled，注入一次 `ErrStaleSnapshot` 后断言终态落库且确实发生重试）与 `TestEngineTerminalStatusDoesNotRetryStaleFence`（注入 `ErrStaleFence` 断言恰好一次写尝试、不重试不强写）。均经 `-race -count=3` 验证。

- **位置**：`internal/application/runtime/runtime_helpers.go`
  - `persistRunCompleted`（L673/L679）→ `saveSnapshotWithRetry`（带 CAS 冲突重试）✅
  - `markRunFailedMode`（L746/L771）、`markRunCancelled`（L779/L790）→ **单次** `saveRunSnapshot` ❌（**已过时**：现均走 `saveSnapshotWithRetry`，见上）
- **现象**：乐观并发下（其他 goroutine/worker 同时推进快照），失败/取消写入撞上 `ErrStaleSnapshot` 直接放弃，仅打日志。
- **后果**：run 的终态（failed/cancelled）丢失，状态停留在 `Running`；若 lease 也未释放/过期，该 run 成为僵尸，只能等 reaper 兜底（若 reaper 未配置则永久滞留）。监控、重试入口（`RetryFailedRun`）也会因状态不对而失效。
- **修复方向**：失败/取消路径统一走 `saveSnapshotWithRetry`；若重试耗尽，升级为 `markRunAbandoned` 或显式强制写（`force=true` 已有雏形，评估直接复用）。

---

## 🟠 中危

### D3 用 `fmt.Sprintf("%q")` 构造 JSON，可能产出非法 JSON

> **状态（2026-08-07）：已修复。**
>
> - 所有产出 JSON 数据的 `%q`/`strconv.Quote` 点已改 `json.Marshal`：
>   - `internal/application/runtime`：新增 `jsonStringValue`（`runtime_helpers.go`），替换 checkpoint 变量（`runtime_continue.go` 的 tool_approval 变量组、`runtime_checkpoint.go` 的 before_final 变量组、`persistResumeTriggerKind`）、resume 元数据（`saveResumeMetadata`）、`run_started_at`、lease owner，以及 `emitJSON` 的 `{"error":%q}` 兜底。
>   - `internal/application/orchestration`：新增 `jsonStringValue`（`workflow.go`），替换 workflow 工具审批 checkpoint 变量（`workflow_tool_approval.go`）与 `emitJSON` 兜底。
>   - 框架根包：`quoteJSONString` 由 `strconv.Quote` 改为 `json.Marshal`（`meta.go`，原实现同样产生 `\xNN` 非法转义），新增 `quoteJSONErrorPayload`；`framework.go`/`framework_workflow.go` 的 `{"error":%q}` 事件 payload 全部改 map marshal。
> - 未动的合法用途：`pkg/graph/codegen.go`（生成 Go 源码字面量）、日志/错误文案中的 `%q`（不产出 JSON）。
> - **回归测试**：`runtime_continue_test.go` `TestEngineCheckpointVariablesRoundTripUnsafeStrings`（控制字符/引号/反斜杠/unicode 经 checkpoint + resume 变量 round-trip，且全部快照变量 `json.Valid`）；`meta_internal_test.go` `TestQuoteJSONStringEscapesForJSON` / `TestQuoteJSONErrorPayloadEscapesForJSON`。修复前红（`%q` 产出 `\x01` 非法转义）。

- **位置**：final output 组装、`saveCheckpointVariables`、部分 event payload、`variableString` 兜底等多处。（**注**：final output 组装在当前代码已是 `json.Marshal`（`runtime_continue.go` `completeRun`），不属残留点。）
- **现象**：Go 的 `%q` 面向 Go 字符串字面量而非 JSON：对控制字符会用 `\xNN`/`\u` 混合转义，`\x` 在 JSON 中非法；含 rune 字面量场景同理。
- **后果**：final output / checkpoint variables / event payload 变成非法 JSON，下游（resume、观测、客户端解析）报错；`variableString` 兜底还会把带引号的原始串直接当值返回，进一步污染类型。
- **修复方向**：全局搜索 `"%q"` 与 `fmt.Sprintf` 拼 JSON 的点，统一改 `json.Marshal`/`json.MarshalToString`；为 checkpoint variables 增加 round-trip 测试。

### D4 HITL 批准恢复路径未应用 `ToolOutputMaxBytes` 截断

> **状态（2026-08-07）：经核实，截断语义已被上轮重构修复；本次完成路径统一并补回归测试。**
>
> - 当前代码 `continueToolLoopFrom`（`runtime_continue.go`）对 approved 工具结果已调用 `materializeToolResultForContext`（`runtime_tool_batch.go:206`），该函数同时做 `compactToolResultForContext` + `ToolOutputMaxBytes` 双截断，报告的"只 compact 不截断"已不成立。
> - 残留差异（本次修复）：恢复路径此前手工拼 `llm.Message`，缺批路径 `materializeToolBatchItem` 附带的 `tool_result_class`/`truncate_strategy` metadata。已改为直接复用 `materializeToolBatchItem` 构造 approved 工具消息，两条路径完全同一物化函数。
> - **回归测试**：`runtime_continue_test.go` `TestEngineContinueAfterApprovalAppliesToolOutputMaxBytes`（批准恢复 + 1024B 大输出 + `ToolOutputMaxBytes=64`：断言 LLM 所见 tool 消息被截断且带截断 metadata，step output 保留完整结果；旧代码因缺 metadata 红）。

- **位置**：`runtime_continue.go` `continueToolLoopFrom` / `dispatchApprovedTool` vs `runtime_tool_batch.go` `materializeToolBatchItem`
- **现象**：正常批处理路径会按 `ToolOutputMaxBytes` 截断工具输出；审批批准后经 `ContinueAfterCheckpoint` 恢复的路径只调用 `compactToolResultForContext`，缺了字节上限截断。
- **后果**：大输出工具（读大文件、抓日志）走审批路径时，超大结果直接进上下文，撑爆 token 预算甚至触发 LLM 侧错误。
- **修复方向**：抽一个共用的 `materializeToolResult(cfg, result)`，两条路径统一调用；补测试覆盖"批准恢复 + 超大输出"。

### D5 审批暂停-恢复后，tool turn memory 可能漏写

> **状态（2026-08-07）：经核实已被上轮重构修复，复核通过。**
>
> - 当前代码 `continueToolLoopFrom`（`runtime_continue.go`）在执行完 approved 调用 + `pending[1:]` 之后，调用 `persistToolTurnFromStepOutputs`（`runtime_memory.go:116`）：对整个 assistant turn 的每个 tool_call 从 step outputs 重建 compacted tool 消息，连同 assistant 消息原子写入 memory。approved 调用经 `dispatchApprovedTool`（非 skipPersist）持久化 `tool.<callID>` step output，批内其余调用由 `executeToolBatch` 统一 `saveStepOutputs`，两者都能被该函数覆盖；再次暂停时 memory 尚未写入、checkpoint 重记剩余 pending，最终一次 resume 补全整轮，无重复无遗漏。
> - **回归测试（已有）**：`runtime_continue_test.go` `TestEngineToolLoopMemoryUnchangedWhilePaused`（暂停在批中间 → 恢复 → memory 含 user + assistant(tool_calls) + 两条 tool 结果 + final assistant，共 5 条）。2026-08-07 复核 `-count=1` 通过。

- **位置**：`runtime_continue.go` 恢复路径对 `pending[1:]` 传 `persistTurnMemory=false`，只写 StepOutputs。（**注**：该行之后已有 `persistToolTurnFromStepOutputs` 兜底整轮持久化，见上。）
- **现象**：暂停发生在批中间时，已执行部分的 assistant 消息 + tool results 只进了 step outputs，未进 conversation memory。
- **后果**：后续轮次 `prepareMessages` 从 memory 重建上下文时缺这一段工具结果，模型看到"工具没执行过"，可能重复调用或推理错乱；memory defect 类测试可复现。
- **修复方向**：恢复路径对整批（含 pending 前后）持久化完整 tool turn memory；或明确"step outputs 是唯一事实源"并在 `prepareMessages` 中合并回放。

### D6 detached stream 取消观察者高频轮询授权表

- **位置**：detached stream 的 cancellation watcher（framework.go / runtime stream 相关）。
- **现象**：每 100ms 全量 `LoadAuthorized` 一次，用于检测授权撤销。
- **后果**：长时运行的 streaming run 会在整个生命周期内对授权存储形成稳定高频读压力；多 run 并发时放大。
- **修复方向**：延长间隔（如 5–10s）+ 指数退避；或改为事件/订阅式失效通知；至少把间隔提为可配置项。

### D7 checkpoint 写入与 gate.Pause 之间崩溃 → 悬挂 checkpoint；恢复不校验 token

> **状态（2026-08-07）：已修复（pending_pause 标记 + fail-closed 恢复守卫 + reaper 清理 + token 化恢复入口）。**
>
> - **pending_pause 标记**：`maybePauseToolCall` / `pauseBeforeFinalAnswer` 写 checkpoint 变量时同时置 `checkpoint_pending_pause`（键名导出为 `runstate.VarCheckpointPendingPause`）。标记**不在** pause 成功后立即清除——那会推进快照版本、使刚签发的 pause token 失效（`ErrTokenSuperseded`）；改由确认批准的路径清除：`internal/adapter/human/cli` gate 的 `Resume` 在同一次 save 内原子删除；`Framework.Resume` / `resumeAndContinueLocked` 在 `gate.Resume` 成功后兜底清除（覆盖自定义 gate）。
> - **fail-closed 守卫**：引擎 `continueAfterCheckpoint` 拒绝带标记的 Running 快照（未获批准的 checkpoint 永不执行），报错指引 `ClearCheckpointState`；`clearCheckpointVariables` 键表已含标记键，pause 失败回滚/正常清理路径行为不变（既有回滚测试覆盖）。
> - **reaper 清理 + 可观测**：`markRunAbandoned`（`lease.go`）对带标记的 run 调 `appexec.ClearUnconfirmedCheckpoint` 一并丢弃未批准 checkpoint（防止 `RetryFailedRun` 执行未批准状态），失败原因记为 `"worker lost (unconfirmed pause checkpoint discarded)"`，`EventRunFailed` payload 带 `unconfirmed_checkpoint_discarded:true` 并告警日志；无标记的已批准 checkpoint 照常保留可恢复。
> - **token 化恢复**：新增 `Framework.ContinueRunWithToken(ctx, runID, token)`——经 `TokenSigner.Verify`（HMAC 签名 + runID 绑定 + TTL）或 gate 的 `core.PauseTokenDecoder` 校验后才走原 `ContinueRun` 逻辑；不比对 token 内版本号（gate.Resume 合法推进版本）。`ContinueRun` 签名不变（向后兼容），文档标注其不带凭证、仅供受信调用方。
> - **行为变更须知**：绕开 Framework 直接 `gate.Resume` + `engine.ContinueAfterCheckpoint` 的树外集成，若使用自定义 gate，需在 resume 后调用 `engine.ClearPendingPauseMarker`（内置 cli gate 与所有 Framework resume 入口已自动处理）。
> - **回归测试**：`runtime_continue_test.go` `TestEnginePauseCarriesPendingPauseMarkerUntilResume`（标记生命周期）、`TestEngineContinueRefusesUnconfirmedPauseCheckpoint`（守卫拒绝 + 状态不变 + 清理后解除）；`framework_pending_pause_test.go` `TestFrameworkReaperDiscardsUnconfirmedPauseCheckpoint`（有/无标记两僵尸对照）、`TestFrameworkContinueRunRefusesUnconfirmedPauseCheckpoint`、`TestFrameworkContinueRunWithToken`（无 token/伪 token/他 run token/过期 token 均拒，正确 token 完成）。均 `-count=1` 通过；runtime 包过 `-race`。

- **位置**：`maybePauseToolCall` / `pauseBeforeFinalAnswer`（写 checkpoint）→ `gate.Pause`（发 token）；`ContinueAfterCheckpoint` 只接受 runID。
- **现象**：checkpoint 已落库但进程在 `gate.Pause` 返回前崩溃，DB 中留下 `Running + tool_approval checkpoint`；另外引擎层恢复只按 runID 找 checkpoint，不校验调用方持有的 gate token。
- **后果**：①悬挂 run 无 token 可续，只能靠 reaper；②任何知道 runID 的调用方都能替别人批准（越权恢复风险）。
- **修复方向**：写 checkpoint 时置 `pending_pause` 标记，`gate.Pause` 成功后清标记，reaper 清理带 `pending_pause` 的悬挂态；`ContinueAfterCheckpoint` 增加 token/审批凭证校验参数。

---

## 🟡 低危

| 编号 | 位置 | 问题 | 修复方向 |
|---|---|---|---|
| D8 | `persistRunCompleted` → `emit(EventRunCompleted)` | final 输出外置 blob 后用 `finalRef.Inline`（可能为 nil）作为 payload，关键生命周期事件无内容 | 事件携带 blob 引用（key/size）而非内联内容 |
| D9 | `ensureRunPaused` | 单次 CAS，失败仅告警；可能出现 gate 已发 token 但状态仍 Running | 复用 `saveSnapshotWithRetry`，失败则不发放 token |
| D10 | `answerWithToolsFrom` | 循环内 `toolSpecs := e.toolSpecs(...)` 遮蔽外层参数，`replanOrFail` 可能拿到旧 specs | 重命名/上提变量，replan 统一取最新 specs |
| D11 | `Framework.Stream` | 调用方不消费也不 cancel 返回的流时，forwarder、engine goroutine、lease renewer 永久阻塞（文档已声明义务但无兜底） | 加 idle 超时/心跳检测，或 detach 时强制带 deadline |
| D12 | `executeToolWithRetry` 早退分支 | 工具出错且 ctx 已取消时直接返回，`orchestrator.AfterAttempt` 被跳过，审批缓存的 attempt 统计不完整 | 用 `defer` 保证 AfterAttempt 至少记录一次（带 cancelled 标记） |
| D13 | `persistRunCompleted` | 先 `blobs.Put` 再重试保存快照；重试全失败则 blob 成孤儿 | 依赖 blob GC（确认 GC 存在并覆盖该路径），或失败时 best-effort 删除 blob |

---

## 修复路线图建议

1. **P0（本迭代）**：D1（原子 checkAndReserve + 并发回归测试）、D2（失败/取消走 `saveSnapshotWithRetry`）。
2. **P1（下一迭代）**：D3 全局替换 `json.Marshal`；D4/D5 统一恢复路径的 materialize 与 memory 持久化（同一批改动，天然耦合）。
3. **P2**：D7/D9 checkpoint 生命周期治理（pending_pause + token 校验）；D6 轮询降频。
4. **P3**：D8、D10–D13 随常规排期清理。

## 值得肯定的设计

- RunState 乐观并发 + `saveSnapshotWithRetry` + `ErrStaleSnapshot` 的整体思路正确，问题只在**路径覆盖不全**。
- Lease fencing（`WithRunLease`/`ErrRunLeaseLost`/`ErrStaleFence`）+ reaper 为多实例部署提供了较完整的僵尸防护。
- `auto:iter:<n>` 迭代快照让 autonomous 循环具备崩溃恢复能力。
- path lock 对同路径文件工具串行化，避免了并发写文件的常见坑。
