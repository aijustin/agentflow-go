package agentflow_test

import (
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
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

func TestValidateWiringDevelopmentOptions(t *testing.T) {
	scenario, err := agentflow.LoadScenarioFile("examples/autonomous.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := agentflow.DevelopmentOptions(scenario, agentflow.DevelopmentConfig{WorkDir: "examples"})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		t.Fatal(err)
	}
}

func TestWithRequireLLM(t *testing.T) {
	scenario, err := agentflow.LoadScenarioFile("examples/autonomous.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = agentflow.New(scenario, agentflow.WithRequireLLM())
	if err == nil {
		t.Fatal("expected require LLM error")
	}
}

func TestScenarioJSONSchema(t *testing.T) {
	schema := agentflow.ScenarioJSONSchema()
	if len(schema) == 0 {
		t.Fatal("expected non-empty schema")
	}
}
