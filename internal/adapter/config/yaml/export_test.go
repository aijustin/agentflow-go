package yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestMarshalRoundTripPreservesLegacyDocumentShape(t *testing.T) {
	initial, err := Load([]byte(`
scenario:
  name: roundtrip
  llms:
    default:
      provider: mock
      model: test
  memories:
    session:
      type: in_memory
      scope: session
      namespace: roundtrip-session
  tools:
    echo:
      type: builtin.echo
      approval: never
  agents:
    worker:
      llm: default
      memory: session
      tools: [echo]
      instructions: help
  runtime:
    validate_tool_input: true
    hitl_deny_limit: 4
    max_total_tokens: 12345
  orchestration:
    mode: fixed_workflow
    workflow:
      nodes:
        - id: prep
          kind: transform
          input:
            set:
              x: 1
      edges: []
`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "scenario:\n") {
		t.Fatalf("expected legacy scenario root, got:\n%s", string(data))
	}
	loaded, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "roundtrip" {
		t.Fatalf("unexpected name %q", loaded.Name)
	}
	if loaded.Orchestration.Workflow == nil || len(loaded.Orchestration.Workflow.Nodes) != 1 {
		t.Fatalf("unexpected workflow: %+v", loaded.Orchestration.Workflow)
	}
	if !loaded.Runtime.ValidateToolInput || loaded.Runtime.DisableToolInputValidation || loaded.Runtime.HITLDenyLimit != 4 {
		t.Fatalf("runtime fields did not round-trip: %+v", loaded.Runtime)
	}
	if loaded.Runtime.MaxTotalTokens != 12345 {
		t.Fatalf("max_total_tokens did not round-trip: %+v", loaded.Runtime)
	}
}

func TestMarshalRoundTripPreservesToolValidationOptOut(t *testing.T) {
	scenario := core.Scenario{
		Name: "runtime-opt-out",
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
		},
		Agents: map[string]core.Agent{
			"worker": {Name: "worker", LLM: "default"},
		},
		Runtime:       core.RuntimePolicy{DisableToolInputValidation: true},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	data, err := Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Runtime.DisableToolInputValidation {
		t.Fatalf("runtime validation opt-out did not round-trip: %+v", loaded.Runtime)
	}
}

func TestSaveFileWritesScenarioDocument(t *testing.T) {
	scenario := core.Scenario{
		Name: "save-test",
		Agents: map[string]core.Agent{
			"worker": {Name: "worker", LLM: "default"},
		},
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationAutonomous,
		},
	}
	path := filepath.Join(t.TempDir(), "nested", "scenario.yaml")
	if err := SaveFile(path, scenario); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "scenario:") || !strings.Contains(string(data), "save-test") {
		t.Fatalf("unexpected file contents: %s", string(data))
	}
}
