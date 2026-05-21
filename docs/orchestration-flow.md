# Agent 编排逻辑流程图

本文档描述 agentflow-go 从 Scenario YAML 加载到 Run 完成的完整执行链路，涵盖 **Skills 展开、LLM 调用、Tools 执行、三种编排模式、HITL 暂停/恢复**。

相关代码入口见文末「关键代码索引」。

---

## 一、总览：从 YAML 到 Run 完成

```mermaid
flowchart TB
    subgraph S1["① 配置加载与展开"]
        YAML["scenario.yaml"]
        LOAD["LoadScenarioFile<br/>configyaml.Load → ToCore"]
        VAL1["Validate<br/>引用/Workflow DAG 校验"]
        BUILD["appscenario.Build"]
        SK1["expandSkills<br/>instructions + policy + tool policies"]
        SK2["expandWorkflowSkillNodes<br/>skill 子图 inline"]
        VAL2["validateNoSkillNodes"]
        NEW["Framework.New<br/>toolRegistry + Engine + Gate + Memory"]
        YAML --> LOAD --> VAL1 --> BUILD
        BUILD --> SK1 --> SK2 --> VAL2 --> NEW
    end

    subgraph S2["② Run 入口"]
        ENTRY{"入口类型"}
        RUN["Framework.Run"]
        EVT["HandleEvent<br/>trigger → RunRequest"]
        RAC["ResumeAndContinue"]
        ENTRY --> RUN
        ENTRY --> EVT --> RUN
        ENTRY --> RAC
    end

    subgraph S3["③ 编排模式分发"]
        MODE{"orchestration.mode"}
        AUTO["autonomous<br/>Engine.Run"]
        FW["fixed_workflow<br/>WorkflowRunner.Run"]
        HYB["hybrid<br/>Workflow → RunHybrid"]
        RUN --> MODE
        MODE -->|autonomous| AUTO
        MODE -->|fixed_workflow| FW
        MODE -->|hybrid| HYB
    end

    NEW --> ENTRY
```

---

## 二、Skills 展开逻辑（Build 阶段）

Skills **不是运行时 Actor**，而是在 `Build` 时**编译进 Scenario**：

```mermaid
flowchart LR
    subgraph AgentSkills["Agent 引用 skills[]"]
        A["Agent.assistant<br/>skills: [code_review]"]
        S["Skill.code_review"]
        A --> S
    end

    subgraph Expand["expandSkills()"]
        P["mergePromptFragments<br/>→ agent.Instructions"]
        POL["mergeAgentPolicy<br/>→ MaxSteps/Timeout/OutputSchema/HITL"]
        TP["applySkillToolPolicies<br/>→ 覆盖 scenario.Tools 的 Approval/SideEffect"]
        WF["Skill.Workflow<br/>→ 追加 nodes/edges 到 orchestration.workflow<br/>ID 前缀: agent.skill."]
        S --> P
        S --> POL
        S --> TP
        S --> WF
    end

    subgraph Inline["expandWorkflowSkillNodes()"]
        NS["workflow 中 kind=skill 节点"]
        IN["inline 子图 + 重连依赖边"]
        OUT["runtime 前无 NodeSkill 残留"]
        NS --> IN --> OUT
    end

    Expand --> Inline
```

**要点：**

- Skill → **Prompt 片段 + Agent 策略 + Tool 策略 + 可选 Workflow 子图**
- Workflow 里的 `skill` 节点在 Build 时展开，运行时只认识 `tool / agent / human_gate / ...`

---

## 三、Autonomous 模式：LLM + Tools 核心循环

```mermaid
flowchart TB
    START["Engine.Run"] --> SNAP["创建/保存 RunSnapshot<br/>status=running"]
    SNAP --> AGENT["resolveAgent(req.Agent)"]
    AGENT --> HITL1{"HITL enabled?<br/>before_final_answer"}
    HITL1 -->|是| PAUSE1["gate.Pause → Paused + Token"]
    HITL1 -->|否| ANSWER["Engine.answer"]

    subgraph ANSWER_BLOCK["answer() 内部"]
        MEM["readMemory<br/>按 scope 读历史消息"]
        CTX["prepareContext<br/>system + memory + context JSON + prompt"]
        CW["contextwindow.Manager.Prepare<br/>滑动窗口/压缩 tool 结果"]
        PLAN{"planning.enabled?"}
        PLAN_INJ["injectAutonomousPlan<br/>LLM 生成 plan → 注入 system"]
        BRANCH{"agent 有 tools/sub_agents<br/>且 LLM 支持 CapToolCall?"}

        MEM --> CTX --> CW --> PLAN
        PLAN -->|是| PLAN_INJ --> BRANCH
        PLAN -->|否| BRANCH

        BRANCH -->|否| CHAT["chatWithRetry → llm.Chat"]
        BRANCH -->|是| LOOP["answerWithToolsFrom<br/>最多 maxSteps 轮"]

        subgraph TOOL_LOOP["Tool Loop"]
            L1["prepareMessages"]
            L2["llm.ChatWithTools"]
            L3{"有 tool_calls?"}
            L4["dispatchTool"]
            L5["append tool result → 下一轮"]
            L1 --> L2 --> L3
            L3 -->|否| DONE["返回最终文本"]
            L3 -->|是| L4 --> L5 --> L1
        end

        LOOP --> TOOL_LOOP
        CHAT --> WRITEMEM["writeMemory"]
        DONE --> WRITEMEM
    end

    ANSWER --> ANSWER_BLOCK
    WRITEMEM --> FINAL["StepOutputs[final]<br/>status=completed"]
```

### dispatchTool 完整链路

```mermaid
flowchart TB
    TC["ToolCall"] --> SUB{"delegate_* ?"}
    SUB -->|是| SA["dispatchSubAgent<br/>递归 answer()"]
    SUB -->|否| ALLOW["agentAllowsTool 白名单"]
    ALLOW --> MANIFEST["scenario.Tools[name]"]
    MANIFEST --> APPROVAL{"approval 策略"}
    APPROVAL -->|always/risky| DENY["直接拒绝"]
    APPROVAL -->|pause + 有 gate| PAUSE["maybePauseToolCall<br/>→ tool_approval 暂停"]
    APPROVAL -->|never/通过| SEC["security.Policy.Authorize"]
    SEC --> GOV["governance.ToolPolicy.AuthorizeTool"]
    GOV --> RESOLVE["toolRegistry.ResolveTool"]
    RESOLVE --> EXEC["executor.Execute<br/>HTTP/SQL/Git/Ticket/MCP/..."]
    EXEC --> SAVE["saveStepOutput + writeMemory + audit/event"]
```

**Tool 解析优先级**（`toolRegistry.ResolveTool`）：

1. `WithToolExecutor` 显式注册（eager）
2. 缓存（cache）
3. `WithToolResolver` 动态解析

---

## 四、Fixed Workflow / Hybrid 模式

```mermaid
flowchart TB
    subgraph FW_MODE["fixed_workflow"]
        FWR["runWorkflow"] --> WRR["WorkflowRunner.Run"]
        WRR --> DAG["DAG 拓扑调度<br/>readyNodes → runBatch<br/>parallel ≤ maxParallel"]
    end

    subgraph HYB_MODE["hybrid"]
        HR["runHybrid"] --> PHASE{"execution_phase"}
        PHASE -->|workflow| WRR2["WorkflowRunner.Run"]
        WRR2 --> SWITCH["phase → autonomous"]
        SWITCH --> HYDR["hydrateRunRequest<br/>StepOutputs → req.Context"]
        HYDR --> ERH["Engine.RunHybrid"]
        PHASE -->|无 workflow| ER["Engine.Run"]
    end

    subgraph NODE_DISPATCH["runNode() 按 kind 分发"]
        DAG --> ND{"node.kind"}
        ND -->|tool| TN["runToolNode<br/>ResolveTool → Execute"]
        ND -->|agent| AN["runAgentNode<br/>Engine.RunAgent → answer()"]
        ND -->|transform| TR["runTransformNode<br/>copy_from / set"]
        ND -->|human_gate| HG["runHumanGateNode<br/>gate.Pause"]
        ND -->|parallel_group| PG["并行跑多个 agent/tool"]
        ND -->|loop| LP["迭代 body 直到 until/max"]
        HG --> WP["WorkflowPausedError"]
    end
```

**Workflow 与 Autonomous 的区别：**

| | fixed_workflow / hybrid（阶段 1） | autonomous |
|---|---|---|
| 调度 | DAG 节点顺序/并行/条件边 | LLM 自主决定 tool_calls |
| LLM | 仅在 `agent` 节点调用 | 每轮 answer/tool loop |
| Tool | `runToolNode` 直接 Execute | `dispatchTool` 经 LLM 决策 |
| 输出 | 各 step 写入 `StepOutputs[nodeID]` | `StepOutputs["final"]` + `tool.*` |

---

## 五、LLM Gateway 调用链

```mermaid
flowchart LR
    AG["Agent.LLM<br/>→ scenario.LLMs[profile]"] --> CAP{"Supports(capability)?"}
    CAP --> CHAT["Gateway.Chat<br/>普通对话"]
    CAP --> TOOLS["ToolCaller.ChatWithTools<br/>tool loop"]
    CAP --> STRUCT["StructuredOutputter.StructuredChat<br/>RunStructured / planning"]
    CAP --> STREAM["Streamer.StreamChat<br/>Framework.Stream"]

    subgraph PROVIDERS["Provider 实现"]
        OAI["OpenAI-compatible"]
        ANT["Anthropic"]
        LOC["Local"]
        RTR["LLM Router<br/>按 profile 路由"]
    end

    CHAT --> PROVIDERS
    TOOLS --> PROVIDERS
    STRUCT --> PROVIDERS
    STREAM --> PROVIDERS
```

**消息组装顺序**（`prepareContext`）：

1. `system`: agent.Instructions（已含 Skill 展开的 prompt）
2. memory 历史（受 `memory_recall_limit` 限制）
3. `user`: Runtime context JSON（hybrid 阶段 2 含 workflow step outputs）
4. `user`: prompt

---

## 六、HITL 暂停与恢复

```mermaid
flowchart TB
    subgraph PAUSE_POINTS["三种暂停点"]
        P1["before_final_answer<br/>Engine.Run 末尾前"]
        P2["tool_approval<br/>dispatchTool 前 approval=pause"]
        P3["human_gate 节点<br/>WorkflowRunner"]
    end

    P1 --> GATE["humancli.Gate.Pause<br/>snapshot→paused<br/>PendingGate + HMAC Token"]
    P2 --> GATE
    P3 --> GATE

    GATE --> USER["人工决策 approve/reject/amend"]

    USER --> RESUME{"API"}
    RESUME --> R1["Resume<br/>仅更新 snapshot"]
    RESUME --> R2["ResumeAndContinue<br/>Resume + 继续执行"]

    R2 --> CONT{"continueRun 路由"}
    CONT -->|fixed_workflow| WR["WorkflowRunner.Resume"]
    CONT -->|hybrid| HWR["先 workflow Resume<br/>再 RunHybrid"]
    CONT -->|autonomous| EC["Engine.ContinueAfterCheckpoint<br/>before_final / tool_approval 分支"]
```

---

## 七、端到端时序（Autonomous + Tools + Skill 已展开）

```mermaid
sequenceDiagram
    autonumber
    participant U as 调用方
    participant F as Framework
    participant B as Build/Skills
    participant E as Engine
    participant M as Memory/Context
    participant L as LLM Gateway
    participant T as ToolRegistry
    participant X as ToolExecutor

    U->>F: LoadScenarioFile(yaml)
    F->>B: Build → expandSkills + inline skill nodes
    B-->>F: 展开后的 Scenario
    U->>F: New(scenario, WithLLMGateway, WithToolExecutor...)
    U->>F: Run(prompt, agent)

    F->>E: Engine.Run (autonomous)
    E->>M: readMemory + prepareContext
    Note over M: Instructions 已含 Skill prompt<br/>ContextWindow 裁剪
    E->>L: ChatWithTools (若有 tools)
    L-->>E: tool_calls
    E->>T: ResolveTool(name)
    T-->>E: executor
    E->>X: Execute(input)
    X-->>E: ToolResult
    E->>M: writeMemory + saveStepOutput
    E->>L: ChatWithTools (下一轮...)
    L-->>E: 最终文本
    E-->>F: RunResult{Output, Status}
    F-->>U: 完成
```

---

## 八、三种模式对比

```mermaid
flowchart TB
    subgraph AUTO["autonomous"]
        A1["Prompt"] --> A2["LLM ↔ Tool Loop"] --> A3["final output"]
    end

    subgraph FIX["fixed_workflow"]
        F1["DAG: tool → agent → transform"] --> F2["human_gate?"] --> F3["workflow done"]
    end

    subgraph HYB["hybrid"]
        H1["Phase1: Workflow DAG"] --> H2["hydrate context"] --> H3["Phase2: LLM ↔ Tool Loop"] --> H4["final output"]
    end

    SK["Skills 在 Build 时展开<br/>影响 Instructions/Tools/Workflow"] -.-> AUTO
    SK -.-> FIX
    SK -.-> HYB
```

---

## 九、实例：`code_review_pipeline.yaml`

`examples/code_review_pipeline.yaml` 是 **fixed_workflow** 模式，对应第四节节点调度：

```mermaid
flowchart LR
    DIFF["diff<br/>kind: tool → git diff"] --> REV["reviews<br/>kind: parallel_group<br/>security + style agents"]
    REV --> MERGE["merge<br/>kind: transform<br/>copy findings"]
    MERGE --> APPROVE["approve<br/>kind: human_gate"]
    APPROVE -->|ResumeAndContinue| DONE["workflow completed"]
```

---

## 关键代码索引

| 阶段 | 文件 | 核心函数 |
|------|------|----------|
| YAML 加载 | `internal/adapter/config/yaml/config.go` | `LoadFile` |
| Skills 展开 | `internal/application/scenario/builder.go` | `expandSkills`, `expandWorkflowSkillNodes` |
| Run 分发 | `framework.go` | `Framework.Run` |
| 自主模式 | `internal/application/runtime/runtime.go` | `Engine.Run`, `answer` |
| LLM+Tool 循环 | `internal/application/runtime/runtime_llm.go` | `answerWithToolsFrom` |
| Tool 执行 | `internal/application/runtime/runtime_tools.go` | `dispatchTool` |
| Workflow | `internal/application/orchestration/workflow.go` | `WorkflowRunner.Run`, `runNode` |
| Hybrid 恢复 | `framework_continue.go` | `ResumeAndContinue`, `continueHybridRun` |
| 事件触发 | `framework_event.go` | `HandleEvent` |
| LLM 接口 | `pkg/llm/types.go` | `Gateway`, `ToolCaller`, `StructuredOutputter` |
| HITL Gate | `internal/adapter/human/cli/gate.go` | `Pause`, `Resume` |
| 上下文窗口 | `pkg/contextwindow/manager.go` | `Manager.Prepare` |
