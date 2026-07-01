package agentflow_test

import (
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func TestValidateWiringRequiresToolExecutor(t *testing.T) {
	scenario := core.Scenario{
		Name: "wiring-test",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Tools: map[string]core.Tool{
			"http_call": {Name: "http_call", Type: "http.client"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	err := agentflow.ValidateWiring(scenario)
	if err == nil {
		t.Fatal("expected missing tool executor error")
	}
}

func TestValidateWiringTestOptions(t *testing.T) {
	scenario := builder.MinimalAutonomous("assistant")
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: "."})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		t.Fatal(err)
	}
}

func TestWithRequireLLM(t *testing.T) {
	scenario := builder.MinimalAutonomous("assistant")
	_, err := agentflow.New(scenario, agentflow.WithRequireLLM())
	if err == nil {
		t.Fatal("expected require LLM error")
	}
}

func TestFrameworkNewValidatesMemoryWiring(t *testing.T) {
	scenario := core.Scenario{
		Name: "wiring-test",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Memories: map[string]core.MemoryRef{
			"postgres_mem": {Type: "postgres"},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	_, err := agentflow.New(scenario, agentflow.WithToolExecutor("echo", noopTool{}))
	if err == nil {
		t.Fatal("expected missing memory repository error from Framework.New")
	}
}

func TestScenarioJSONSchema(t *testing.T) {
	schema := agentflow.ScenarioJSONSchema()
	if len(schema) == 0 {
		t.Fatal("expected non-empty schema")
	}
}
