# Configuration Reference

AgentFlow applications are primarily built from scenario YAML. This reference lists the supported configuration sections, important fields, and enum values. For editor completion and CI validation, use the machine-readable schema at [`schemas/agentflow.scenario.schema.json`](../schemas/agentflow.scenario.schema.json).

```yaml
# yaml-language-server: $schema=../schemas/agentflow.scenario.schema.json
scenario:
  name: support-assistant
  agents:
    assistant:
      instructions: "Answer with approved tools only."
```

Print the schema from the CLI:

```sh
agentctl schema
agentctl schema --format json
```

Validate a scenario file:

```sh
agentctl validate -f scenario.yaml
```

## Top-Level Shape

All configuration lives under one `scenario:` root.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `scenario.name` | string | yes | Stable scenario identifier. |
| `scenario.description` | string | no | Human-readable purpose and scope. |
| `scenario.llms` | map | no | Named LLM profiles. |
| `scenario.memories` | map | no | Named memory repositories and scopes. |
| `scenario.tools` | map | no | Named tool contracts. |
| `scenario.skills` | map | no | Declarative prompt, policy, and workflow packages. |
| `scenario.agents` | map | yes | Named agents runnable by the runtime. |
| `scenario.orchestration` | object | no | Runtime orchestration mode and workflow graph. |
| `scenario.runtime` | object | no | Global runtime limits and operational settings. |

## LLM Profiles

Profiles are declared under `scenario.llms.<name>` and referenced by agents or tools.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `provider` | string | yes | Provider identifier. Built-in examples include `mock`, `openai-compatible`, `anthropic`, and `local`; applications may register custom gateways. |
| `model` | string | yes | Provider-specific model name. |
| `endpoint` | string | no | Provider endpoint, commonly used by OpenAI-compatible servers. |
| `api_key_env` | string | no | Environment variable containing the API key. |
| `context_window_tokens` | integer | no | Advertised profile context window. |
| `max_output_tokens` | integer | no | Maximum generated output tokens. |
| `temperature` | number | no | Sampling temperature. |
| `top_p` | number | no | Nucleus sampling value. |
| `timeout` | duration | no | Go-style duration such as `30s` or `2m`. |
| `thinking.enabled` | boolean | no | Enables provider-specific reasoning mode. |
| `thinking.budget_tokens` | integer | no | Token budget for reasoning-capable providers. |
| `reasoning_effort` | string | no | Provider-specific reasoning effort hint, for example `low`, `medium`, or `high`. |
| `context` | object | no | Context window policy. |
| `extra_body` | object | no | Provider-specific request body extensions. |
| `capabilities` | string array | no | Explicit capability list. |
| `metadata` | string map | no | Operator-defined metadata. |

Supported `capabilities` values:

| Value | Meaning |
| --- | --- |
| `chat` | Standard chat completion. |
| `tool_call` | Model can request tool calls. |
| `structured_output` | Model can produce schema-constrained output. |
| `stream` | Model can stream chat chunks. |
| `embed` | Model can produce embeddings. |

Supported `context.strategy` values:

| Value | Meaning |
| --- | --- |
| `none` | No trimming strategy beyond direct request construction. |
| `sliding_window` | Keep protected prompts and recent messages within budget. |
| `sliding_window_with_summary` | Summarize dropped context and keep recent messages within budget. |

Context policy fields: `context_window_tokens`, `max_input_tokens`, `reserved_output_tokens`, `summary_tokens`, `tool_result_max_tokens`, `memory_recall_limit`, `system_prompt_protection`, and `compression.trigger_ratio`.

## Memories

Memories are declared under `scenario.memories.<name>` and referenced by agents.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `type` | enum | yes | Memory backend type. |
| `scope` | enum | yes | Memory namespace scope. |
| `namespace` | string | no | Optional logical namespace. |
| `metadata` | string map | no | Operator-defined metadata. |

Supported `type` values:

| Value | Meaning |
| --- | --- |
| `in_memory` | Ephemeral in-process memory. |
| `file` | File-backed memory repository wired by the host. |
| `custom` | Host application provides the repository. |

Supported `scope` values:

| Value | Meaning |
| --- | --- |
| `conversation` | Short-lived conversation memory. |
| `session` | Session memory. |
| `long_term` | Long-term memory namespace. |
| `audit` | Audit-oriented memory namespace. |

## Tools

Tools are declared under `scenario.tools.<name>` and become available only to agents that list them explicitly.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `type` | string | yes | Tool executor type. Built-in examples include `builtin.echo`, `builtin.http`, `builtin.filesystem`, `builtin.sql`, and `mcp.tool`; applications may register custom tools. |
| `description` | string | no | Human-readable tool purpose. |
| `input_schema` | object | no | JSON Schema fragment for tool input. |
| `output_schema` | object | no | JSON Schema fragment for tool output. |
| `side_effect` | enum | no | Declared side-effect level for governance. |
| `approval` | enum | no | Approval policy before execution. |
| `llm` | string | no | Optional LLM profile override. |
| `rate_cap` | integer | no | Maximum calls per run. |
| `metadata` | string map | no | Operator-defined metadata. |

Supported `approval` values:

| Value | Meaning |
| --- | --- |
| `never` | No human approval required. |
| `risky` | Approval may be required by governance for risky execution. |
| `always` | Always require approval. |

Supported `side_effect` values:

| Value | Meaning |
| --- | --- |
| `none` | No side effects. |
| `read` | Read-only access. |
| `write` | Writes internal state. |
| `external` | Calls external systems. |
| `dangerous` | High-risk or irreversible action. |

## Agents

Agents are declared under `scenario.agents.<name>`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `description` | string | no | Human-readable agent purpose. |
| `role` | string | no | Business role label. |
| `instructions` | string | no | System-level behavior instructions. |
| `llm` | string | no | Name of a profile from `scenario.llms`. |
| `memory` | string | no | Name of a memory entry from `scenario.memories`. |
| `tools` | string array | no | Tool names from `scenario.tools`. |
| `skills` | string array | no | Skill names from `scenario.skills`. |
| `sub_agents` | string array | no | Agents available for delegation. |
| `max_steps` | integer | no | Agent-level step cap. |
| `timeout` | duration | no | Agent-level timeout. |
| `retry_limit` | integer | no | Agent-level retry cap. |
| `output_schema` | object | no | JSON Schema fragment for structured output. |
| `human_checkpoints` | string array | no | Agent-specific HITL checkpoint names. |
| `metadata` | string map | no | Operator-defined metadata. |

## Orchestration

Orchestration is declared under `scenario.orchestration`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `mode` | enum | no | Runtime orchestration mode. Empty mode defaults to autonomous behavior. |
| `workflow` | object | required for `fixed_workflow` | Workflow graph. |
| `max_parallel` | integer | no | Maximum parallel workflow batch size. |
| `human_in_loop.enabled` | boolean | no | Enables global HITL checkpoints. |
| `human_in_loop.checkpoints` | string array | no | Global checkpoint names. |

Supported `mode` values:

| Value | Meaning |
| --- | --- |
| `autonomous` | LLM-driven tool loop. |
| `fixed_workflow` | Deterministic workflow graph. |
| `hybrid` | Reserved for mixed workflow and autonomous execution. |

Workflow nodes live under `scenario.orchestration.workflow.nodes`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes | Unique node identifier. |
| `kind` | enum | yes | Node type. |
| `ref` | string | depends on kind | Referenced agent, tool, or skill name. |
| `input` | object | no | Static input payload. |
| `depends_on` | string array | no | Dependency node IDs. |
| `condition` | string | no | Conditional execution expression. |
| `retry.max_attempts` | integer | no | Node retry cap. |

Supported workflow `kind` values:

| Value | Meaning |
| --- | --- |
| `agent` | Run a configured agent. |
| `tool` | Execute a configured tool. |
| `skill` | Reference a configured skill. |
| `human_gate` | Pause for human approval. |
| `transform` | Transform workflow data. |

Workflow edges live under `scenario.orchestration.workflow.edges` and use `from`, `to`, and optional `condition`.

## Runtime

Runtime settings are declared under `scenario.runtime`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `timeout` | duration | no | Global run timeout. |
| `max_steps` | integer | no | Global autonomous step cap. |
| `max_retries` | integer | no | Global retry cap. |
| `max_parallel` | integer | no | Global parallelism cap. |
| `step_output_threshold` | integer | no | Externalize large step outputs above this byte threshold. |
| `secrets` | string map | no | Secret references. Prefer environment variables for sensitive values. |

## Extension Points

Some values are intentionally not strict enums:

| Field | Why |
| --- | --- |
| `llms.*.provider` | Enterprises often route through custom model gateways. |
| `tools.*.type` | Business tools are registered by the host application. |
| `reasoning_effort` | Providers use different labels and semantics. |
| `metadata` | Operators may add deployment, ownership, or compliance labels. |

Use JSON Schema for authoring feedback, `agentctl validate` for runtime references and workflow graph checks, and tests for organization-specific policy rules.