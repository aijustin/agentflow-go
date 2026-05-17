# 配置参考

AgentFlow 应用主要通过场景 YAML 构建。本参考列出支持的配置段、关键字段和枚举值。编辑器补全和 CI 校验可使用机器可读 Schema：[`schemas/agentflow.scenario.schema.json`](../schemas/agentflow.scenario.schema.json)。

```yaml
# yaml-language-server: $schema=../schemas/agentflow.scenario.schema.json
scenario:
  name: support-assistant
  agents:
    assistant:
      instructions: "回答时仅使用获批工具。"
```

通过 CLI 输出 Schema：

```sh
agentctl schema
agentctl schema --format json
```

校验场景文件：

```sh
agentctl validate -f scenario.yaml
```

## 顶层结构

所有配置都位于一个 `scenario:` 根节点下。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `scenario.name` | 字符串 | 是 | 稳定的场景标识。 |
| `scenario.description` | 字符串 | 否 | 场景用途和边界说明。 |
| `scenario.llms` | 映射 | 否 | 命名 LLM Profile。 |
| `scenario.memories` | 映射 | 否 | 命名 Memory 仓库和作用域。 |
| `scenario.tools` | 映射 | 否 | 命名 Tool 契约。 |
| `scenario.skills` | 映射 | 否 | 声明式 prompt、policy 和 workflow 包。 |
| `scenario.agents` | 映射 | 是 | Runtime 可执行的命名 Agent。 |
| `scenario.orchestration` | 对象 | 否 | Runtime 编排模式和工作流图。 |
| `scenario.runtime` | 对象 | 否 | 全局运行限制和运维参数。 |

## LLM 配置

LLM 配置定义在 `scenario.llms.<name>` 下，可被 Agent 或工具引用。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `provider` | 字符串 | 是 | Provider 标识。内置示例包括 `mock`、`openai-compatible`、`anthropic`、`local`；应用也可以注册自定义网关。 |
| `model` | 字符串 | 是 | Provider 侧的模型名称。 |
| `endpoint` | 字符串 | 否 | Provider 端点，常用于 OpenAI-compatible 服务。 |
| `api_key_env` | 字符串 | 否 | 保存 API Key 的环境变量名。 |
| `context_window_tokens` | 整数 | 否 | 该配置声明的上下文窗口大小。 |
| `max_output_tokens` | 整数 | 否 | 允许模型生成的最大输出 token 数。 |
| `temperature` | 数字 | 否 | 采样温度。 |
| `top_p` | 数字 | 否 | nucleus sampling 参数。 |
| `timeout` | duration | 否 | Go 风格 duration，例如 `30s` 或 `2m`。 |
| `thinking.enabled` | 布尔值 | 否 | 启用 Provider 特定的 reasoning / thinking 模式。 |
| `thinking.budget_tokens` | 整数 | 否 | reasoning-capable Provider 的思考 token 预算。 |
| `reasoning_effort` | 字符串 | 否 | Provider 特定的推理强度提示，例如 `low`、`medium`、`high`。 |
| `context` | 对象 | 否 | 上下文窗口治理策略。 |
| `extra_body` | 对象 | 否 | Provider 特定的请求体扩展字段。 |
| `capabilities` | 字符串数组 | 否 | 显式声明该配置支持的能力。 |
| `metadata` | 字符串映射 | 否 | 运维侧自定义元数据。 |

支持的 `capabilities` 值：

| 值 | 含义 |
| --- | --- |
| `chat` | 标准聊天补全。 |
| `tool_call` | 模型可以请求调用工具。 |
| `structured_output` | 模型可以按 schema 生成结构化输出。 |
| `stream` | 模型可以流式输出聊天片段。 |
| `embed` | 模型可以生成 embedding。 |

支持的 `context.strategy` 值：

| 值 | 含义 |
| --- | --- |
| `none` | 不使用额外裁剪策略，直接构造请求上下文。 |
| `sliding_window` | 保留受保护 prompt 和最近消息，使输入落在预算内。 |
| `sliding_window_with_summary` | 对丢弃上下文生成摘要，同时保留最近消息。 |

Context policy 字段包括：`context_window_tokens`、`max_input_tokens`、`reserved_output_tokens`、`summary_tokens`、`tool_result_max_tokens`、`memory_recall_limit`、`system_prompt_protection`、`compression.trigger_ratio`。其中 `tool_result_max_tokens` 会限制工具结果回灌给下一轮 LLM 的上下文大小；完整工具输出仍会按运行状态/Blob 策略持久化。

## 记忆

记忆定义在 `scenario.memories.<name>` 下，可被 Agent 引用。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `type` | 枚举 | 是 | 记忆后端类型。 |
| `scope` | 枚举 | 是 | 记忆命名空间作用域。 |
| `namespace` | 字符串 | 否 | 可选逻辑命名空间。 |
| `metadata` | 字符串映射 | 否 | 运维侧自定义元数据。 |

支持的 `type` 值：

| 值 | 含义 |
| --- | --- |
| `in_memory` | 进程内临时记忆。 |
| `file` | 由宿主应用注入的文件持久化记忆仓库。 |
| `custom` | 宿主应用自行提供记忆仓库。 |

支持的 `scope` 值：

| 值 | 含义 |
| --- | --- |
| `conversation` | 短生命周期会话记忆。 |
| `session` | Session 级记忆。 |
| `long_term` | 长期记忆命名空间。 |
| `audit` | 面向审计的记忆命名空间。 |

## 工具

工具定义在 `scenario.tools.<name>` 下，只有被 Agent 显式列出的工具才能被该 Agent 使用。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `type` | 字符串 | 是 | 工具执行器类型。内置示例包括 `builtin.echo`、`builtin.http`、`builtin.filesystem`、`builtin.sql`、`mcp.tool`；应用也可以注册自定义工具。 |
| `description` | 字符串 | 否 | 工具的用途说明。 |
| `input_schema` | 对象 | 否 | 工具输入的 JSON Schema 片段。 |
| `output_schema` | 对象 | 否 | 工具输出的 JSON Schema 片段。 |
| `side_effect` | 枚举 | 否 | 用于治理判断的副作用等级。 |
| `approval` | 枚举 | 否 | 工具执行前的审批策略。 |
| `llm` | 字符串 | 否 | 可选 LLM 配置覆盖。 |
| `rate_cap` | 整数 | 否 | 单次运行内该工具的最大调用次数。 |
| `metadata` | 字符串映射 | 否 | 运维侧自定义元数据。 |

支持的 `approval` 值：

| 值 | 含义 |
| --- | --- |
| `never` | 不需要人工审批。 |
| `risky` | 由治理策略判断风险场景是否需要审批。 |
| `always` | 每次执行都需要审批。 |

支持的 `side_effect` 值：

| 值 | 含义 |
| --- | --- |
| `none` | 无副作用。 |
| `read` | 只读访问。 |
| `write` | 写入内部状态。 |
| `external` | 调用外部系统。 |
| `dangerous` | 高风险或不可逆动作。 |

## Skill 配置

Skill 定义在 `scenario.skills.<name>` 下。它是可复用的能力包，不是独立运行时 Actor。Agent 通过 `skills` 绑定 Skill 后，场景构建阶段会把 prompt 片段合并到 Agent 指令，覆盖 Agent/Tool 策略，并把 Skill 的 workflow 子图展开到主 workflow 中。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `source` | 字符串 | 否 | Skill 来源，例如本地路径、目录 ID 或内部 catalog 标识；会保存到 metadata 的 `source`。 |
| `description` | 字符串 | 否 | Skill 的用途和边界说明。 |
| `version` | 字符串 | 否 | Skill 包版本。 |
| `compatible_agents` | 字符串数组 | 否 | 该 Skill 预期兼容的 Agent 名称；引用未知 Agent 或绑定到未列出的 Agent 会校验失败。 |
| `prompt_fragments` | 数组 | 否 | 追加到 Agent `instructions` 后的 prompt 片段。 |
| `prompt_fragments[].name` | 字符串 | 否 | prompt 片段名称，会作为段落标题写入。 |
| `prompt_fragments[].content` | 字符串 | 是 | prompt 片段内容。 |
| `agent_policy` | 对象 | 否 | 覆盖绑定 Agent 的执行策略，支持 `max_steps`、`timeout`、`retry_limit`、`output_schema`、`human_checkpoints`。 |
| `tool_policies` | 数组 | 否 | Skill 对工具审批、副作用等级和调用上限的策略覆盖。 |
| `tool_policies[].tool` | 字符串 | 是 | 来自 `scenario.tools` 的工具名称。 |
| `tool_policies[].approval` | 枚举 | 否 | 覆盖该工具的审批策略，支持 `never`、`risky`、`always`。 |
| `tool_policies[].side_effect` | 枚举 | 否 | 覆盖工具副作用等级，支持 `none`、`read`、`write`、`external`、`dangerous`。 |
| `tool_policies[].rate_cap` | 整数 | 否 | 覆盖该工具单次运行内最大调用次数。 |
| `workflow` | 对象 | 否 | 可复用 workflow 子图；展开时节点 ID 会加上 `<agent>.<skill>.` 前缀。 |
| `metadata` | 字符串映射 | 否 | 运维侧自定义元数据。 |

示例：

```yaml
scenario:
  tools:
    sql.query:
      type: builtin.sql
      approval: never
  skills:
    ticket-review:
      source: ./skills/ticket-review
      version: "1.0.0"
      description: 工单答复复核能力包。
      compatible_agents: [assistant]
      prompt_fragments:
        - name: 复核风格
          content: 回答前先确认数据来源，并标注不确定信息。
      agent_policy:
        max_steps: 6
        retry_limit: 1
      tool_policies:
        - tool: sql.query
          approval: risky
          side_effect: read
          rate_cap: 2
      workflow:
        nodes:
          - id: inspect
            kind: tool
            ref: sql.query
          - id: summarize
            kind: transform
            depends_on: [inspect]
        edges:
          - from: inspect
            to: summarize
  agents:
    assistant:
      skills: [ticket-review]
      tools: [sql.query]
```

## Agent 配置

Agent 定义在 `scenario.agents.<name>` 下。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `description` | 字符串 | 否 | Agent 用途说明。 |
| `role` | 字符串 | 否 | 业务角色标签。 |
| `instructions` | 字符串 | 否 | Agent 的系统级行为指令。 |
| `llm` | 字符串 | 否 | 来自 `scenario.llms` 的配置名称。 |
| `memory` | 字符串 | 否 | 来自 `scenario.memories` 的记忆名称。 |
| `tools` | 字符串数组 | 否 | 来自 `scenario.tools` 的工具名称。 |
| `skills` | 字符串数组 | 否 | 来自 `scenario.skills` 的 Skill 名称；绑定后会在场景构建阶段展开。 |
| `sub_agents` | 字符串数组 | 否 | 可用于委派的 Agent。 |
| `max_steps` | 整数 | 否 | Agent 级步骤上限。 |
| `timeout` | duration | 否 | Agent 级超时时间。 |
| `retry_limit` | 整数 | 否 | Agent 级重试上限。 |
| `output_schema` | 对象 | 否 | 结构化输出的 JSON Schema 片段。 |
| `human_checkpoints` | 字符串数组 | 否 | Agent 专属 HITL checkpoint 名称。 |
| `metadata` | 字符串映射 | 否 | 运维侧自定义元数据。 |

## 编排

编排策略定义在 `scenario.orchestration` 下。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `mode` | 枚举 | 否 | 运行时编排模式。为空时按 autonomous 行为处理。 |
| `workflow` | 对象 | `fixed_workflow` 必填 | 工作流图。 |
| `max_parallel` | 整数 | 否 | 工作流批次的最大并行度。 |
| `human_in_loop.enabled` | 布尔值 | 否 | 启用全局 HITL checkpoint。 |
| `human_in_loop.checkpoints` | 字符串数组 | 否 | 全局 checkpoint 名称。 |
| `planning.enabled` | 布尔值 | 否 | 启用自主执行前的规划 pass。 |
| `planning.agent` | 字符串 | 否 | 专门用于生成计划的 Agent；为空时使用当前执行 Agent。 |
| `planning.max_steps` | 整数 | 否 | 规划输出的最大步骤数；`0` 表示使用运行时默认值。 |

支持的 `mode` 值：

| 值 | 含义 |
| --- | --- |
| `autonomous` | LLM 驱动的自主执行，可选 planning pass，随后进入受治理工具调用循环。 |
| `fixed_workflow` | 确定性工作流图。 |
| `hybrid` | 为固定流程与自主执行混合场景预留。 |

Workflow 节点位于 `scenario.orchestration.workflow.nodes`。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `id` | 字符串 | 是 | 唯一节点标识。 |
| `kind` | 枚举 | 是 | 节点类型。 |
| `ref` | 字符串 | 取决于 `kind` | 引用的 Agent、Tool 或 Skill 名称。 |
| `input` | 对象 | 否 | 静态输入载荷。 |
| `depends_on` | 字符串数组 | 否 | 依赖的节点 ID。 |
| `condition` | 字符串 | 否 | 条件执行表达式。 |
| `retry.max_attempts` | 整数 | 否 | 节点重试上限。 |

支持的 workflow `kind` 值：

| 值 | 含义 |
| --- | --- |
| `agent` | 运行一个已配置 Agent。 |
| `tool` | 执行一个已配置 Tool。 |
| `skill` | 引用一个已配置 Skill。 |
| `human_gate` | 暂停并等待人工审批。 |
| `transform` | 转换工作流数据。 |

Workflow 边位于 `scenario.orchestration.workflow.edges`，使用 `from`、`to` 和可选 `condition`。

`condition` 支持以下轻量表达式：

| 表达式 | 含义 |
| --- | --- |
| `true` / `always` / 空 | 执行节点。 |
| `false` / `never` | 跳过节点。 |
| `exists(steps.inspect.output)` | 当路径存在时执行。 |
| `missing(steps.inspect.error)` | 当路径不存在时执行。 |
| `eq(steps.inspect.output.status, "ready")` | 当路径值等于期望值时执行。 |
| `ne(steps.inspect.output.status, "blocked")` | 当路径值不等于期望值时执行。 |

路径以 `steps.<node_id>` 开头，后续字段来自该节点保存到 RunState 的 JSON 输出。Tool 节点输出通常形如 `steps.<id>.output.<field>`，因为 `core.ToolResult` 会把工具原始 JSON 放在 `output` 字段中。

`transform` 节点的 `input` 可以使用 `set` 和 `copy` 构造新的步骤输出：

```yaml
workflow:
  nodes:
    - id: inspect
      kind: tool
      ref: docs.search
    - id: shape
      kind: transform
      depends_on: [inspect]
      input:
        set:
          kind: summary
        copy:
          first_result: steps.inspect.output.results.0.content
```

`set` 写入静态字段，`copy` 从已有步骤输出路径复制字段。没有 `set`/`copy` 的 transform 会保留旧行为：把静态 input 包装为该节点输出。

## 运行时

运行时设置定义在 `scenario.runtime` 下。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `timeout` | duration | 否 | 全局运行超时时间。 |
| `max_steps` | 整数 | 否 | 全局 autonomous 步骤上限。 |
| `max_retries` | 整数 | 否 | 全局重试上限。 |
| `max_parallel` | 整数 | 否 | 全局并行度上限。 |
| `step_output_threshold` | 整数 | 否 | 单步输出超过该字节阈值时外置到已配置的 BlobStore；未配置 BlobStore 或未超过阈值时继续内联保存。 |
| `secrets` | 字符串映射 | 否 | Secret 引用。敏感值建议优先使用环境变量。 |

大输出外置适合 SQL/RAG/长文档生成等大载荷场景，用来控制 RunState 的数据库行大小和 Redis 内存占用。生产环境可以通过 `WithBlobStore` 接入文件、内存或 S3-compatible BlobStore；S3 兼容实现面向 MinIO、AWS S3 path-style endpoint，以及通过 path-style SigV4 `PUT`/`GET` 验证的腾讯云 COS/阿里云 OSS 兼容接口。原生 COS/OSS API 需要独立适配器实现同一个 `runstate.BlobStore` 契约。

## 扩展点

部分字段有意不做严格枚举：

| 字段 | 原因 |
| --- | --- |
| `llms.*.provider` | 企业通常会接入自定义模型网关。 |
| `tools.*.type` | 业务工具由宿主应用注册，框架不应限制业务工具类型。 |
| `reasoning_effort` | 不同 Provider 的标签和语义可能不同。 |
| `metadata` | 运维团队可能加入部署、归属、合规等标签。 |

建议用 JSON Schema 获得编写阶段的字段和枚举提示，用 `agentctl validate` 做运行时引用关系和工作流图校验，再用测试承载组织内部的额外策略规则。