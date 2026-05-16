package debugui

import "strings"

type ScenarioTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	YAML        string `json:"yaml"`
	Prompt      string `json:"prompt"`
	Context     string `json:"context"`
	RealModel   bool   `json:"real_model"`
}

func builtinScenarios() []ScenarioTemplate {
	return []ScenarioTemplate{
		{
			ID:          "autonomous",
			Name:        "Autonomous Mock",
			Description: "LLM-style autonomous flow using the runtime skeleton; without a real LLM it echoes the prompt.",
			Prompt:      "Summarize what this agent framework can do.",
			YAML: `scenario:
  name: debug-autonomous
  llms:
    default:
      provider: mock
      model: test
  memories:
    session:
      type: in_memory
      scope: session
  agents:
    assistant:
      llm: default
      memory: session
      instructions: "Answer clearly and briefly."
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: false
`,
		},
		{
			ID:          "fixed_workflow",
			Name:        "Fixed Workflow",
			Description: "Deterministic workflow graph that runs a builtin echo tool and records step events.",
			Prompt:      "Run the fixed workflow.",
			YAML: `scenario:
  name: debug-fixed-workflow
  llms:
    planner:
      provider: mock
      model: test
  memories:
    session:
      type: in_memory
      scope: session
  tools:
    repo_search:
      type: builtin.repo_search
      approval: never
  agents:
    reviewer:
      llm: planner
      memory: session
      tools: [repo_search]
      instructions: "Review repository changes."
  orchestration:
    mode: fixed_workflow
    workflow:
      nodes:
        - id: inspect
          kind: tool
          ref: repo_search
          input:
            query: "agent framework"
        - id: review
          kind: agent
          ref: reviewer
      edges:
        - from: inspect
          to: review
`,
		},
		{
			ID:          "hitl",
			Name:        "Human In Loop",
			Description: "Pauses before final answer, returns a signed token, then resumes from the browser.",
			Prompt:      "Prepare an answer and wait for approval.",
			YAML: `scenario:
  name: debug-human-in-loop
  llms:
    default:
      provider: mock
      model: test
  memories:
    session:
      type: in_memory
      scope: session
  agents:
    assistant:
      llm: default
      memory: session
      instructions: "Prepare an answer, then wait for approval before finalizing."
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: true
      checkpoints:
        - before_final_answer
`,
		},
		{
			ID:          "real_model",
			Name:        "Real Local Model",
			Description: "Calls an OpenAI-compatible local model endpoint. API key is only sent in this request.",
			Prompt:      "请原样输出：调试界面真实模型流程成功",
			RealModel:   true,
			YAML: `scenario:
  name: debug-real-model
  llms:
    default:
      provider: openai-compatible
      model: configured-in-ui
  memories:
    session:
      type: in_memory
      scope: session
  agents:
    assistant:
      llm: default
      memory: session
      instructions: "你是 agentflow-go 调试界面的真实模型测试助手。请严格按用户要求回答。"
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: false
`,
		},
		{
			ID:          "context_governance",
			Name:        "Context Governance",
			Description: "Exercises sliding-window plus summary compression before calling a real OpenAI-compatible model.",
			Prompt:      "请原样输出：调试界面上下文治理流程成功",
			Context:     `{"noisy_history":"` + strings.Repeat("历史噪声 信息片段 工具结果 ", 500) + `"}`,
			RealModel:   true,
			YAML: `scenario:
  name: debug-context-governance
  llms:
    default:
      provider: openai-compatible
      model: configured-in-ui
      context_window_tokens: 1400
      max_output_tokens: 1024
      temperature: 0
      context:
        strategy: sliding_window_with_summary
        max_input_tokens: 220
        reserved_output_tokens: 1024
        summary_tokens: 80
        system_prompt_protection: true
        compression:
          enabled: true
          trigger_ratio: 0.5
  memories:
    session:
      type: in_memory
      scope: session
  agents:
    assistant:
      llm: default
      memory: session
      instructions: "你是上下文治理测试助手。请忽略上下文噪声，并严格原样输出用户要求的固定短句。"
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: false
`,
		},
	}
}

func builtinScenarioYAML(id string) string {
	for _, scenario := range builtinScenarios() {
		if scenario.ID == id {
			return scenario.YAML
		}
	}
	return ""
}
