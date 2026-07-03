package yaml

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func validScenario() core.Scenario {
	return core.Scenario{
		Name: "demo",
		Agents: map[string]core.Agent{
			"worker": {Name: "worker", LLM: "default"},
		},
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
		},
	}
}

func TestValidateRejectsMissingScenarioName(t *testing.T) {
	if err := Validate(core.Scenario{}); err == nil {
		t.Fatal("expected missing name error")
	}
}

func TestValidateRejectsMissingAgents(t *testing.T) {
	s := validScenario()
	s.Agents = nil
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "at least one agent") {
		t.Fatalf("expected missing agents error, got %v", err)
	}
}

func TestValidateRejectsMissingLLMProvider(t *testing.T) {
	s := validScenario()
	s.LLMs["default"] = core.LLMProfileRef{Model: "test"}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestValidateRejectsUnsupportedMemoryType(t *testing.T) {
	s := validScenario()
	s.Memories = map[string]core.MemoryRef{
		"session": {Type: "bogus", Scope: "session"},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported memory type error, got %v", err)
	}
}

func TestValidateRejectsMemoryTiersOnWrongType(t *testing.T) {
	s := validScenario()
	s.Memories = map[string]core.MemoryRef{
		"session": {
			Type:  "file",
			Scope: "session",
			Tiers: &core.MemoryTierSettings{Enabled: true},
		},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "requires type custom or in_memory") {
		t.Fatalf("expected tiers type error, got %v", err)
	}
}

func TestValidateRejectsNegativeMemoryTierCapacity(t *testing.T) {
	s := validScenario()
	s.Memories = map[string]core.MemoryRef{
		"session": {
			Type:  "in_memory",
			Scope: "session",
			Tiers: &core.MemoryTierSettings{Enabled: true, HotCapacity: -1},
		},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "capacities must be >= 0") {
		t.Fatalf("expected negative capacity error, got %v", err)
	}
}

func TestValidateRejectsToolUnknownLLMRef(t *testing.T) {
	s := validScenario()
	s.Tools = map[string]core.Tool{
		"echo": {Name: "echo", Type: "builtin.echo", LLM: "missing"},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unknown llm") {
		t.Fatalf("expected unknown llm error, got %v", err)
	}
}

func TestValidateRejectsAgentUnknownToolRef(t *testing.T) {
	s := validScenario()
	s.Agents["worker"] = core.Agent{Name: "worker", LLM: "default", Tools: []string{"missing"}}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestValidateRejectsIncompatibleSkillForAgent(t *testing.T) {
	s := validScenario()
	s.Agents["other"] = core.Agent{Name: "other", LLM: "default"}
	s.Skills = map[string]core.Skill{
		"helper": {CompatibleAgents: []string{"other"}},
	}
	s.Agents["worker"] = core.Agent{Name: "worker", LLM: "default", Skills: []string{"helper"}}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "incompatible skill") {
		t.Fatalf("expected incompatible skill error, got %v", err)
	}
}

func TestValidateRejectsUnsupportedOrchestrationMode(t *testing.T) {
	s := validScenario()
	s.Orchestration.Mode = "bogus"
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unsupported orchestration.mode") {
		t.Fatalf("expected unsupported mode error, got %v", err)
	}
}

func TestValidateRejectsFixedWorkflowWithoutWorkflow(t *testing.T) {
	s := validScenario()
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "requires orchestration.workflow") {
		t.Fatalf("expected missing workflow error, got %v", err)
	}
}

func TestValidateRejectsWorkflowMissingNodeID(t *testing.T) {
	s := validScenario()
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	s.Orchestration.Workflow = &core.Workflow{
		Nodes: []core.WorkflowNode{{Kind: core.NodeTransform}},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "node id is required") {
		t.Fatalf("expected missing node id error, got %v", err)
	}
}

func TestValidateRejectsWorkflowCycle(t *testing.T) {
	s := validScenario()
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	s.Orchestration.Workflow = &core.Workflow{
		Nodes: []core.WorkflowNode{
			{ID: "a", Kind: core.NodeTransform},
			{ID: "b", Kind: core.NodeTransform},
		},
		Edges: []core.WorkflowEdge{
			{From: "a", To: "b"},
			{From: "b", To: "a"},
		},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidateRejectsWorkflowSubgraphRef(t *testing.T) {
	s := validScenario()
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	s.Orchestration.Workflow = &core.Workflow{
		Nodes: []core.WorkflowNode{{ID: "sub", Kind: core.NodeSubgraph, Ref: "missing"}},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unknown subgraph") {
		t.Fatalf("expected unknown subgraph error, got %v", err)
	}
}

func TestValidateRejectsMapNodeUnsupportedOnError(t *testing.T) {
	s := validScenario()
	s.Tools = map[string]core.Tool{"echo": {Name: "echo", Type: "builtin.echo"}}
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	s.Orchestration.Workflow = &core.Workflow{
		Nodes: []core.WorkflowNode{{
			ID: "map1", Kind: core.NodeMap,
			Input: json.RawMessage(`{"items_path":"items","branch":{"kind":"tool","ref":"echo"},"on_error":"skip"}`),
		}},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "on_error") {
		t.Fatalf("expected map on_error error, got %v", err)
	}
}

func TestValidateRejectsLoopNodeUnknownBody(t *testing.T) {
	s := validScenario()
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	s.Orchestration.Workflow = &core.Workflow{
		Nodes: []core.WorkflowNode{{
			ID: "loop1", Kind: core.NodeLoop,
			Input: json.RawMessage(`{"body":["missing"]}`),
		}},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unknown body node") {
		t.Fatalf("expected loop body error, got %v", err)
	}
}

func TestValidateRejectsTriggerUnknownAgent(t *testing.T) {
	s := validScenario()
	s.Triggers = []core.Trigger{{Event: "ticket.created", Agent: "missing"}}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown trigger agent error, got %v", err)
	}
}

func TestValidateRejectsParallelGroupDuplicateTools(t *testing.T) {
	s := validScenario()
	s.Tools = map[string]core.Tool{
		"echo": {Name: "echo", Type: "builtin.echo"},
	}
	s.Orchestration.Mode = core.OrchestrationFixedWorkflow
	s.Orchestration.Workflow = &core.Workflow{
		Nodes: []core.WorkflowNode{{
			ID: "pg1", Kind: core.NodeParallelGroup,
			Input: json.RawMessage(`{"tools":["echo","echo"]}`),
		}},
	}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate tools error, got %v", err)
	}
}

func TestValidateRejectsUnsupportedToolApproval(t *testing.T) {
	err := Validate(core.Scenario{
		Name:   "demo",
		Agents: map[string]core.Agent{"worker": {Name: "worker"}},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: "bogus"},
		},
	})
	if err == nil {
		t.Fatal("expected unsupported approval error")
	}
}

func TestValidateRejectsDuplicateTrigger(t *testing.T) {
	err := Validate(core.Scenario{
		Name:   "demo",
		Agents: map[string]core.Agent{"worker": {Name: "worker"}},
		Triggers: []core.Trigger{
			{Event: "ticket.created"},
			{Event: "ticket.created"},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate trigger error")
	}
}
