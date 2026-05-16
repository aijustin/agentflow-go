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

Context policy 字段包括：`context_window_tokens`、`max_input_tokens`、`reserved_output_tokens`、`summary_tokens`、`tool_result_max_tokens`、`memory_recall_limit`、`system_prompt_protection`、`compression.trigger_ratio`。

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
| `approval` | 枚举 | 否 | Tool 执行前的审批策略。 |
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
| `skills` | 字符串数组 | 否 | 来自 `scenario.skills` 的 Skill 名称。 |
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

支持的 `mode` 值：

| 值 | 含义 |
| --- | --- |
| `autonomous` | LLM 驱动的自主工具调用循环。 |
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

## 运行时

运行时设置定义在 `scenario.runtime` 下。

| 字段 | 类型 | 是否必填 | 说明 |
| --- | --- | --- | --- |
| `timeout` | duration | 否 | 全局运行超时时间。 |
| `max_steps` | 整数 | 否 | 全局 autonomous 步骤上限。 |
| `max_retries` | 整数 | 否 | 全局重试上限。 |
| `max_parallel` | 整数 | 否 | 全局并行度上限。 |
| `step_output_threshold` | 整数 | 否 | 单步输出超过该字节阈值时外置到 BlobStore。 |
| `secrets` | 字符串映射 | 否 | Secret 引用。敏感值建议优先使用环境变量。 |

## 扩展点

部分字段有意不做严格枚举：

| 字段 | 原因 |
| --- | --- |
| `llms.*.provider` | 企业通常会接入自定义模型网关。 |
| `tools.*.type` | 业务工具由宿主应用注册，框架不应限制业务工具类型。 |
| `reasoning_effort` | 不同 Provider 的标签和语义可能不同。 |
| `metadata` | 运维团队可能加入部署、归属、合规等标签。 |

建议用 JSON Schema 获得编写阶段的字段和枚举提示，用 `agentctl validate` 做运行时引用关系和工作流图校验，再用测试承载组织内部的额外策略规则。