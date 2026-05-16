package yaml

import "testing"

func TestLoadValidScenario(t *testing.T) {
	scenario, err := Load([]byte(`
scenario:
  name: test
  llms:
    default:
      provider: mock
      model: test
  memories:
    session:
      type: in_memory
      scope: session
  tools:
    echo:
      type: builtin.echo
      approval: never
      rate_cap: 3
  skills:
    helper:
      source: local
      version: "1.0.0"
  agents:
    worker:
      llm: default
      memory: session
      tools: [echo]
      skills: [helper]
      instructions: help
      timeout: 5s
      retry_limit: 2
      output_schema:
        type: object
        properties:
          answer:
            type: string
  orchestration:
    mode: autonomous
`))
	if err != nil {
		t.Fatal(err)
	}

	if scenario.Name != "test" {
		t.Fatalf("unexpected scenario name %q", scenario.Name)
	}
	if scenario.Tools["echo"].RateCap != 3 {
		t.Fatalf("unexpected tool rate cap: %+v", scenario.Tools["echo"])
	}
	if scenario.Agents["worker"].Policy.Timeout == 0 || scenario.Agents["worker"].Policy.RetryLimit != 2 {
		t.Fatalf("unexpected agent policy: %+v", scenario.Agents["worker"].Policy)
	}
	if len(scenario.Agents["worker"].Policy.OutputSchema) == 0 {
		t.Fatalf("expected agent output schema")
	}
}

func TestLoadLLMRuntimeAndContextConfig(t *testing.T) {
	scenario, err := Load([]byte(`
scenario:
  name: test
  llms:
    default:
      provider: openai-compatible
      model: qwen
      endpoint: http://localhost:1234/v1
      context_window_tokens: 128000
      max_output_tokens: 4096
      temperature: 0.2
      top_p: 0.8
      timeout: 30s
      thinking:
        enabled: true
        budget_tokens: 8192
      reasoning_effort: high
      context:
        strategy: sliding_window_with_summary
        max_input_tokens: 100000
        reserved_output_tokens: 4096
        summary_tokens: 2048
        compression:
          enabled: true
          trigger_ratio: 0.85
        tool_result_max_tokens: 6000
        memory_recall_limit: 12
        system_prompt_protection: true
      extra_body:
        chat_template_kwargs:
          enable_thinking: true
  agents:
    worker:
      llm: default
`))
	if err != nil {
		t.Fatal(err)
	}
	profile := scenario.LLMs["default"]
	if profile.ContextWindowTokens != 128000 || profile.MaxOutputTokens != 4096 {
		t.Fatalf("unexpected token config: %+v", profile)
	}
	if profile.Temperature == nil || *profile.Temperature != 0.2 {
		t.Fatalf("unexpected temperature: %+v", profile.Temperature)
	}
	if !profile.Thinking.Enabled || profile.Thinking.BudgetTokens != 8192 {
		t.Fatalf("unexpected thinking config: %+v", profile.Thinking)
	}
	if profile.Context.Strategy != "sliding_window_with_summary" || !profile.Context.SystemPromptProtection {
		t.Fatalf("unexpected context policy: %+v", profile.Context)
	}
	if profile.ExtraBody["chat_template_kwargs"] == nil {
		t.Fatalf("expected extra body: %+v", profile.ExtraBody)
	}
}

func TestLoadRejectsUnknownToolReference(t *testing.T) {
	_, err := Load([]byte(`
scenario:
  name: test
  agents:
    worker:
      tools: [missing]
`))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing scenario name",
			body: `
	scenario:
	  agents:
	    worker: {}
	`,
		},
		{
			name: "unknown agent llm",
			body: `
	scenario:
	  name: test
	  agents:
	    worker:
	      llm: missing
	`,
		},
		{
			name: "unknown agent memory",
			body: `
	scenario:
	  name: test
	  agents:
	    worker:
	      memory: missing
	`,
		},
		{
			name: "unknown skill",
			body: `
	scenario:
	  name: test
	  agents:
	    worker:
	      skills: [missing]
	`,
		},
		{
			name: "tool references unknown llm",
			body: `
	scenario:
	  name: test
	  tools:
	    echo:
	      type: builtin.echo
	      llm: missing
	  agents:
	    worker: {}
	`,
		},
		{
			name: "unsupported orchestration mode",
			body: `
	scenario:
	  name: test
	  agents:
	    worker: {}
	  orchestration:
	    mode: unsupported
	`,
		},
		{
			name: "fixed workflow missing workflow",
			body: `
	scenario:
	  name: test
	  agents:
	    worker: {}
	  orchestration:
	    mode: fixed_workflow
	`,
		},
		{
			name: "workflow dangling edge",
			body: `
	scenario:
	  name: test
	  agents:
	    worker: {}
	  orchestration:
	    mode: fixed_workflow
	    workflow:
	      nodes:
	        - id: a
	          kind: agent
	      edges:
	        - from: a
	          to: missing
	`,
		},
		{
			name: "negative tool rate cap",
			body: `
	scenario:
	  name: test
	  tools:
	    echo:
	      type: builtin.echo
	      rate_cap: -1
	  agents:
	    worker: {}
	`,
		},
		{
			name: "unsupported memory type",
			body: `
	scenario:
	  name: test
	  memories:
	    session:
	      type: unsupported
	      scope: session
	  agents:
	    worker: {}
	`,
		},
		{
			name: "unsupported memory scope",
			body: `
	scenario:
	  name: test
	  memories:
	    session:
	      type: in_memory
	      scope: unsupported
	  agents:
	    worker: {}
	`,
		},
		{
			name: "unsupported tool approval",
			body: `
	scenario:
	  name: test
	  tools:
	    echo:
	      type: builtin.echo
	      approval: maybe
	  agents:
	    worker: {}
	`,
		},
		{
			name: "workflow tool references unknown tool",
			body: `
	scenario:
	  name: test
	  agents:
	    worker: {}
	  orchestration:
	    mode: fixed_workflow
	    workflow:
	      nodes:
	        - id: missing-tool
	          kind: tool
	          ref: missing
	`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load([]byte(tt.body)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadRejectsFixedWorkflowCycle(t *testing.T) {
	_, err := Load([]byte(`
scenario:
  name: test
  agents:
    worker: {}
  orchestration:
    mode: fixed_workflow
    workflow:
      nodes:
        - id: a
          kind: agent
        - id: b
          kind: agent
      edges:
        - from: a
          to: b
        - from: b
          to: a
`))
	if err == nil {
		t.Fatal("expected cycle validation error")
	}
}
