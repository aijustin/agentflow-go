package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func patchTestBase() core.Scenario {
	return core.Scenario{
		Name: "patch-base",
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
		},
		Agents: map[string]core.Agent{
			"reviewer": {Name: "reviewer", LLM: "default"},
		},
		Skills: map[string]core.Skill{
			"triage": {Name: "triage", Kind: core.SkillKindPrompt},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "start", Kind: core.NodeTransform}},
			},
			Workflows: map[string]core.Workflow{
				"sub": {Nodes: []core.WorkflowNode{{ID: "sub-node", Kind: core.NodeTransform}}},
			},
		},
	}
}

func TestApplyScenarioPatchAddsNewPartsAndWorkflow(t *testing.T) {
	base := patchTestBase()
	patch := ScenarioPatch{
		Agents: map[string]core.Agent{
			"writer": {Description: "drafts text", LLM: "default"},
		},
		Skills: map[string]core.Skill{
			"summarize": {Kind: core.SkillKindPrompt, PromptFragments: []core.PromptFragment{{Content: "summarize"}}},
		},
		Workflow: &GraphView{
			Nodes: []GraphNode{
				{ID: "a", Kind: string(core.NodeAgent), Ref: "writer"},
				{ID: "b", Kind: string(core.NodeAgent), Ref: "reviewer"},
			},
			Edges: []GraphEdge{{From: "a", To: "b"}},
		},
	}
	out, err := ApplyScenarioPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if out.Agents["writer"].Name != "writer" || out.Skills["summarize"].Name != "summarize" {
		t.Fatalf("expected names backfilled: %+v %+v", out.Agents["writer"], out.Skills["summarize"])
	}
	if out.Orchestration.Workflow == nil || len(out.Orchestration.Workflow.Nodes) != 2 {
		t.Fatalf("expected replaced workflow, got %+v", out.Orchestration.Workflow)
	}
	// Base must stay untouched: patch applies to a deep copy.
	if _, ok := base.Agents["writer"]; ok {
		t.Fatal("base scenario mutated by patch")
	}
	if len(base.Orchestration.Workflow.Nodes) != 1 || base.Orchestration.Workflow.Nodes[0].ID != "start" {
		t.Fatalf("base workflow mutated: %+v", base.Orchestration.Workflow)
	}
}

func TestApplyScenarioPatchRejectsExistingIDs(t *testing.T) {
	base := patchTestBase()
	cases := []ScenarioPatch{
		{Agents: map[string]core.Agent{"reviewer": {}}},
		{Skills: map[string]core.Skill{"triage": {}}},
		{Workflows: map[string]GraphView{"sub": {Nodes: []GraphNode{{ID: "n", Kind: string(core.NodeTransform)}}}}},
	}
	for i, patch := range cases {
		if _, err := ApplyScenarioPatch(base, patch); err == nil {
			t.Fatalf("case %d: expected overwrite rejection", i)
		} else if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("case %d: unexpected error: %v", i, err)
		}
	}
}

func TestApplyScenarioPatchAddsNewSubgraphAndSetsMode(t *testing.T) {
	base := patchTestBase()
	patch := ScenarioPatch{
		Mode: string(core.OrchestrationHybrid),
		Workflows: map[string]GraphView{
			"extra": {Nodes: []GraphNode{{ID: "n", Kind: string(core.NodeTransform), Input: json.RawMessage(`{"set":{"x":1}}`)}}},
		},
	}
	out, err := ApplyScenarioPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if out.Orchestration.Mode != core.OrchestrationHybrid {
		t.Fatalf("expected hybrid mode, got %q", out.Orchestration.Mode)
	}
	if len(out.Orchestration.Workflows) != 2 {
		t.Fatalf("expected existing + new subgraph, got %+v", out.Orchestration.Workflows)
	}
	if _, ok := base.Orchestration.Workflows["extra"]; ok {
		t.Fatal("base named workflows mutated by patch")
	}
}

func TestDeepCopyScenarioIsolatesNestedMaps(t *testing.T) {
	base := patchTestBase()
	copied, err := DeepCopyScenario(base)
	if err != nil {
		t.Fatal(err)
	}
	copied.Orchestration.Workflows["sub"] = core.Workflow{}
	copied.Agents["reviewer"] = core.Agent{Name: "hijacked"}
	if len(base.Orchestration.Workflows) != 1 {
		t.Fatal("deep copy shares workflows map with base")
	}
	if base.Agents["reviewer"].Name != "reviewer" {
		t.Fatal("deep copy shares agents map with base")
	}
}
