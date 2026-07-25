# AI 自动构图（ComposeGraph）用法指南

`Studio.ComposeGraph` 把一句话任务变成一张**经过完整校验**的场景图，可选地立即 ephemeral 执行。生成器是一个带构图工具的 agentic composer：它通过 `compose_*` 工具循环逐步搭建 DAG，非法引用、环、越权操作在每一步当场被拒绝并反馈给模型修正（编译反馈回路），而不是单轮生成一大段 JSON。

| 模式 | 生成内容 | 适用场景 |
|------|----------|----------|
| `catalog`（默认） | 仅拓扑（nodes/edges），只引用已注册的 Agent/Tool/Skill/子图 | 零件齐全，只想让 AI 重新编排 |
| `scenario` | 拓扑 + 新建 Agent / prompt Skill（增量 patch，**拒绝覆盖已有 ID**） | 零件不够，需要 AI 补新角色 |

> 内部机制（composer 工具清单、临时 engine、合并管线）见 [orchestration-flow.md](orchestration-flow.md) 第十一节；本文只讲怎么调。

## 前提条件

1. 场景里至少有一个 LLM profile。composer 默认用名为 `default` 的 profile，没有则取排序后的第一个，或用 `ComposerLLM` 显式指定。
2. 注册的 LLM gateway 必须支持 **tool-call**（`llm.CapToolCall`，OpenAI 兼容 / Anthropic gateway 均支持）。composer 不靠 structured output 工作。
3. `Run=true` 时按普通 run 对待：会写 run-state 仓库与事件流，RunID 冲突语义与 `Run` 一致。

## 快速开始

```go
scenario := builder.MinimalGraphComposer("assistant") // 或你自己的场景
fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gateway))
if err != nil { log.Fatal(err) }
defer fw.Close(context.Background())

result, err := fw.ComposeGraph(ctx, agentflow.ComposeGraphRequest{
    Prompt: "先准备主题，再让 assistant 写一段介绍",
    // Mode 省略 = catalog：只编排已有零件
})
if err != nil {
    log.Fatal(err) // 调用级错误（参数、引擎构建、scenario 模式跑 hybrid 等）
}
if !result.Valid {
    log.Fatalf("构图失败: %s", result.Error) // 业务失败：composer 未收敛 / 校验未过
}
fmt.Println(result.Graph.Workflow.Nodes) // 校验后的拓扑
```

可运行示例：`go run ./examples/go/compose-graph`（mock gateway 演示 catalog 与 scenario 两条路径）。

## ComposeGraphRequest

| 字段 | 类型 | 说明 |
|------|------|------|
| `Prompt` | `string` | 必填。一句话任务描述，直接作为 composer 的用户指令 |
| `Mode` | `ComposeMode` | `catalog`（默认）/ `scenario` |
| `ComposerLLM` | `string` | composer 用的 LLM profile 名；空则 `default` 或首个 profile |
| `MaxSteps` | `int` | composer 工具循环步数上限，默认 15。复杂图可调大 |
| `Run` | `bool` | `true` 时校验通过后立即 ephemeral 执行 |
| `RunRequest` | `RunRequest` | 透传给执行：`RunID`、`Prompt`、`Context`、`TrustMode` 等 |

## ComposeGraphResult

| 字段 | 说明 |
|------|------|
| `Valid` / `Error` | 构图是否收敛并通过全量校验；失败时 `Error` 是原因（如 "composer did not call compose_finish"） |
| `Graph` | 合并后场景导出的拓扑（`graph.ScenarioGraph`） |
| `Scenario` | 合并后的**临时**场景指针。永远不会被安装为 live 场景 |
| `Run` | `Run=true` 且校验通过时的执行结果 |

错误分两层：**Go error** = 调用级问题（参数非法、引擎构建失败、scenario 模式对 hybrid 图请求 Run）；**`Valid=false`** = 构图业务失败（composer 没收敛、最终校验未过），不算异常。

## 两种模式的边界

### catalog（默认）

- composer 只能用 `compose_list_parts` 查到的已有零件；新建 Agent/Skill 的工具不开放。
- 产物只替换主 workflow 拓扑；`Run` 走现有 `RunStudioGraph`，`fixed_workflow` 与 `hybrid` 都支持。

### scenario

- 额外开放 `compose_add_agent` / `compose_add_skill`：
  - 新 Agent 的 `llm` 默认继承 `ComposerLLM`；只能绑定**已有 Tool** 和已有/新建 Skill。
  - 与基座同名的 Agent/Skill/子图会被拒绝（AI 不能篡改宿主零件）。
- **新 Tool 声明了也跑不起来**——除非宿主有 `WithToolExecutor` / `WithToolResolver` 能绑定它，否则校验/执行正常失败，不做降级。
- `Run` 在临时 engine + 临时 workflow runner 上执行（新 Agent 才能解析），**仅支持 `fixed_workflow`**；对 hybrid 图请求 Run 会返回明确错误。catalog 模式无此限制。

### 共用约束

- 可生成的节点 kind 白名单：`agent`、`tool`、`skill`、`transform`、`human_gate`、`parallel_group`、`loop`、`subgraph`（`query_router` / `rag_grade` / `map` / `supervisor` 暂不开放）。
- 边条件支持 `exists(...)` / `missing(...)` / `eq(...)` / `ne(...)`，路径为 `steps.<node_id>...`，与现网一致。
- 不生成 `autonomous` 跑图；`compose_finish` 可指定 `fixed_workflow` 或 `hybrid`，缺省继承基座模式（基座是 autonomous 时落到 `fixed_workflow`）。

## 执行与持久化

- **ephemeral 是默认**：合并发生在 live 场景的深拷贝上，live scenario / engine 全程不变。composer 自己的 run 记录以 `<RunID>-compose` 命名。
- **持久化必须显式**：
  - 推荐 `fw.SaveStudioGraph(ctx, result.Graph, "scenario.yaml")`（catalog 模式；YAML 含完整场景）。
  - scenario 模式含新增 Agent/Skill，请基于 `result.Scenario` 走 YAML 序列化（`GenerateStudioScenarioYAML`）落盘。**注意**：`GenerateStudioBuilderCode` 目前只渲染 workflow 结构，不输出 agents/tools/LLM，scenario 模式的新增零件会丢失（codegen 扩展是后续项）。
- 场景真源仍是 Go：落盘后建议检入并由 builder 重建，不要把 AI 产物当运行期主配置。

## 调参建议

- 构图失败先看 `result.Error`；想观察 composer 的决策过程，按普通 run 查 `<RunID>-compose` 的事件流（工具调用与校验反馈都在里面）。
- 零件很多时，composer 会用 `compose_list_parts` 的 `kind`/`query` 过滤而不是一次拉全量——零件描述（`Description`）写得越清楚，匹配越准。
- 图复杂（多分支/子图）时把 `MaxSteps` 调到 20~30。

## 当前限制（v1）

- 仅 Go API；Studio UI「AI 生成」按钮、`POST /studio/compose`、CLI 子命令是后续薄适配。
- scenario 模式 Run 仅 `fixed_workflow`；catalog 模式两种模式都可跑。
- 无自动多候选搜索/打分（AFlow 式选优是 v2 方向）。

## 相关文档

- 机制详解：[orchestration-flow.md](orchestration-flow.md) 第十一节
- 示例代码：[examples/go/compose-graph](../examples/go/compose-graph/main.go)
- 变更记录：[CHANGELOG.md](../CHANGELOG.md)（Unreleased）
