package agentflow_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/adapters"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func composeTestScenario() core.Scenario {
	return core.Scenario{
		Name: "compose-test",
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Description: "repeat the input"},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}, Instructions: "help"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "start", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"seed":true}}`)},
				},
			},
		},
	}
}

func composeTestGateway() *llmmock.FallbackGateway {
	gateway := llmmock.NewFallbackGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	return gateway
}

func queueComposeCall(gateway *llmmock.FallbackGateway, id, tool, input string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: id, Name: tool, Input: json.RawMessage(input)}},
	})
}

func queueComposeAnswer(gateway *llmmock.FallbackGateway) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "graph ready"}},
	})
}

func TestComposeGraphCatalogSuccess(t *testing.T) {
	gateway := composeTestGateway()
	queueComposeCall(gateway, "c1", "compose_list_parts", `{}`)
	queueComposeCall(gateway, "c2", "compose_add_node", `{"id":"a","kind":"transform","input":{"set":{"x":1}}}`)
	queueComposeCall(gateway, "c3", "compose_add_node", `{"id":"b","kind":"transform","input":{"set":{"y":2}}}`)
	queueComposeCall(gateway, "c4", "compose_connect", `{"from":"a","to":"b"}`)
	queueComposeCall(gateway, "c5", "compose_validate", `{}`)
	queueComposeCall(gateway, "c6", "compose_finish", `{}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "build a two-step transform pipeline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected valid composition, got %+v", result)
	}
	if result.Mode != agentflow.ComposeModeCatalog {
		t.Fatalf("expected catalog default mode, got %q", result.Mode)
	}
	if result.Graph.Workflow == nil || len(result.Graph.Workflow.Nodes) != 2 || len(result.Graph.Workflow.Edges) != 1 {
		t.Fatalf("unexpected composed graph: %+v", result.Graph.Workflow)
	}
	if result.Scenario == nil || result.Scenario.Orchestration.Mode != core.OrchestrationFixedWorkflow {
		t.Fatalf("unexpected merged scenario: %+v", result.Scenario)
	}
	// Composition is ephemeral: the live scenario must be untouched.
	live := fw.ExportScenarioGraph()
	if live.Workflow == nil || len(live.Workflow.Nodes) != 1 || live.Workflow.Nodes[0].ID != "start" {
		t.Fatalf("live scenario mutated by compose: %+v", live.Workflow)
	}
}

func TestComposeGraphCatalogUnknownRefFeedback(t *testing.T) {
	gateway := composeTestGateway()
	// First attempt references an unknown tool: the compose tool rejects it
	// with feedback, and the composer corrects itself on the next turn.
	queueComposeCall(gateway, "c1", "compose_add_node", `{"id":"bad","kind":"tool","ref":"missing_tool"}`)
	queueComposeCall(gateway, "c2", "compose_add_node", `{"id":"a","kind":"transform","input":{"set":{"x":1}}}`)
	queueComposeCall(gateway, "c3", "compose_finish", `{}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "wire a tool step",
		Mode:   agentflow.ComposeModeCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected composer to recover from unknown ref feedback, got %+v", result)
	}
	if len(result.Graph.Workflow.Nodes) != 1 || result.Graph.Workflow.Nodes[0].ID != "a" {
		t.Fatalf("unexpected graph after recovery: %+v", result.Graph.Workflow)
	}
}

func TestComposeGraphScenarioAddsAgentAndRuns(t *testing.T) {
	gateway := composeTestGateway()
	queueComposeCall(gateway, "c1", "compose_add_agent", `{"name":"writer","instructions":"draft a short text"}`)
	queueComposeCall(gateway, "c2", "compose_add_node", `{"id":"draft","kind":"agent","ref":"writer"}`)
	queueComposeCall(gateway, "c3", "compose_add_node", `{"id":"mark","kind":"transform","input":{"set":{"done":true}}}`)
	queueComposeCall(gateway, "c4", "compose_connect", `{"from":"draft","to":"mark"}`)
	queueComposeCall(gateway, "c5", "compose_finish", `{}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithRunStateRepository(adapters.NewInMemoryRunStateRepository()),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt:     "draft then mark done",
		Mode:       agentflow.ComposeModeScenario,
		Run:        true,
		RunRequest: agentflow.RunRequest{RunID: "compose-run-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected valid composition, got %+v", result)
	}
	if result.Run == nil || result.Run.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed ephemeral run, got %+v", result.Run)
	}
	if _, ok := result.Scenario.Agents["writer"]; !ok {
		t.Fatalf("merged scenario missing new agent: %+v", result.Scenario.Agents)
	}
	// The new agent must not leak into the live scenario: the live graph
	// still shows only the base workflow.
	live := fw.ExportScenarioGraph()
	if live.Workflow == nil || len(live.Workflow.Nodes) != 1 || live.Workflow.Nodes[0].ID != "start" {
		t.Fatalf("live scenario mutated by compose run: %+v", live.Workflow)
	}
}

func TestComposeGraphScenarioRejectsOverwrite(t *testing.T) {
	gateway := composeTestGateway()
	queueComposeCall(gateway, "c1", "compose_add_agent", `{"name":"assistant","instructions":"hijacked"}`)
	queueComposeCall(gateway, "c2", "compose_add_node", `{"id":"a","kind":"transform","input":{"set":{"x":1}}}`)
	queueComposeCall(gateway, "c3", "compose_finish", `{}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "replace the assistant",
		Mode:   agentflow.ComposeModeScenario,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected composer to recover from overwrite rejection, got %+v", result)
	}
	if result.Scenario.Agents["assistant"].Instructions != "help" {
		t.Fatalf("base agent was overwritten: %+v", result.Scenario.Agents["assistant"])
	}
}

func TestComposeGraphScenarioRunRejectsHybrid(t *testing.T) {
	gateway := composeTestGateway()
	queueComposeCall(gateway, "c1", "compose_add_node", `{"id":"a","kind":"transform","input":{"set":{"x":1}}}`)
	queueComposeCall(gateway, "c2", "compose_finish", `{"mode":"hybrid"}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "run a hybrid graph",
		Mode:   agentflow.ComposeModeScenario,
		Run:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "fixed_workflow") {
		t.Fatalf("expected fixed_workflow-only run error, got %v", err)
	}
}

func TestComposeGraphRequestValidation(t *testing.T) {
	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(composeTestGateway()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{}); err == nil {
		t.Fatal("expected prompt-required error")
	}
	if _, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{Prompt: "x", Mode: "weird"}); err == nil {
		t.Fatal("expected unsupported-mode error")
	}
	if _, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt:      "x",
		ComposerLLM: "missing-profile",
	}); err == nil || !strings.Contains(err.Error(), "composer llm") {
		t.Fatalf("expected missing composer llm error, got %v", err)
	}
}

func TestComposeGraphCatalogEditTools(t *testing.T) {
	gateway := composeTestGateway()
	queueComposeCall(gateway, "c1", "compose_add_node", `{"id":"a","kind":"transform","input":{"set":{"x":1}}}`)
	queueComposeCall(gateway, "c2", "compose_add_node", `{"id":"b","kind":"transform","input":{"set":{"y":2}},"depends_on":["a"]}`)
	queueComposeCall(gateway, "c3", "compose_connect", `{"from":"a","to":"b"}`)
	queueComposeCall(gateway, "c4", "compose_set_input", `{"id":"a","input":{"set":{"x":9}}}`)
	queueComposeCall(gateway, "c5", "compose_disconnect", `{"from":"a","to":"b"}`)
	queueComposeCall(gateway, "c6", "compose_remove_node", `{"id":"b"}`)
	queueComposeCall(gateway, "c7", "compose_add_node", `{"id":"c","kind":"transform","input":{"set":{"z":3}}}`)
	queueComposeCall(gateway, "c8", "compose_connect", `{"from":"a","to":"c"}`)
	queueComposeCall(gateway, "c9", "compose_finish", `{}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "edit nodes then finish",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected valid composition, got %+v", result)
	}
	nodes := result.Graph.Workflow.Nodes
	if len(nodes) != 2 {
		t.Fatalf("expected nodes a,c after edits, got %+v", nodes)
	}
	ids := map[string]bool{}
	for _, node := range nodes {
		ids[node.ID] = true
		if node.ID == "a" && !strings.Contains(string(node.Input), `"x":9`) {
			t.Fatalf("expected set_input on a, got %s", node.Input)
		}
	}
	if !ids["a"] || !ids["c"] || ids["b"] {
		t.Fatalf("unexpected node set after remove/reconnect: %+v", nodes)
	}
}

func TestComposeGraphScenarioAddSkill(t *testing.T) {
	gateway := composeTestGateway()
	queueComposeCall(gateway, "c1", "compose_add_skill", `{"name":"brief","description":"short brief","prompt":"keep answers short"}`)
	queueComposeCall(gateway, "c2", "compose_list_parts", `{"kind":"skill"}`)
	queueComposeCall(gateway, "c3", "compose_add_agent", `{"name":"writer","instructions":"use brief skill","skills":["brief"]}`)
	queueComposeCall(gateway, "c4", "compose_add_node", `{"id":"use_skill","kind":"skill","ref":"brief"}`)
	queueComposeCall(gateway, "c5", "compose_add_node", `{"id":"draft","kind":"agent","ref":"writer"}`)
	queueComposeCall(gateway, "c6", "compose_connect", `{"from":"use_skill","to":"draft"}`)
	queueComposeCall(gateway, "c7", "compose_finish", `{}`)
	queueComposeAnswer(gateway)

	fw, err := agentflow.New(composeTestScenario(), agentflow.WithLLMGateway(gateway))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "add a skill and wire it",
		Mode:   agentflow.ComposeModeScenario,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected valid composition, got %+v", result)
	}
	if _, ok := result.Scenario.Skills["brief"]; !ok {
		t.Fatalf("merged scenario missing skill: %+v", result.Scenario.Skills)
	}
	if writer, ok := result.Scenario.Agents["writer"]; !ok || len(writer.Skills) != 1 || writer.Skills[0] != "brief" {
		t.Fatalf("writer should reference brief skill: %+v", result.Scenario.Agents)
	}
}
