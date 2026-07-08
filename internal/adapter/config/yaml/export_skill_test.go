package yaml

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestDocumentFromCoreExportsSkillWorkflowAndTierMemory(t *testing.T) {
	scenario := core.Scenario{
		Name: "export-rich",
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
		},
		Memories: map[string]core.MemoryRef{
			"session": {
				Type:      "in_memory",
				Scope:     "session",
				Namespace: "export-session",
				Tiers: &core.MemoryTierSettings{
					Enabled:      true,
					HotCapacity:  10,
					HotTTL:       "1h",
					WarmTTL:      "24h",
					DemoteIdle:   "30m",
					RecallBudget: core.MemoryTierRecallBudget{Total: 5, Hot: 2},
				},
			},
		},
		Skills: map[string]core.Skill{
			"research": {
				Name:        "research",
				Description: "Research skill",
				Workflow: &core.Workflow{
					Nodes: []core.WorkflowNode{
						{ID: "prep", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
					},
				},
				Metadata: map[string]string{"source": "local", "team": "ops"},
			},
		},
		Agents: map[string]core.Agent{
			"worker": {Name: "worker", LLM: "default", Memory: "session", Skills: []string{"research"}},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
		Runtime: core.RuntimePolicy{
			Timeout: 5 * time.Minute,
		},
	}
	doc, err := DocumentFromCore(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Scenario.Memories["session"].Tiers == nil || doc.Scenario.Memories["session"].Tiers.HotTTL != time.Hour {
		t.Fatalf("unexpected memory tiers: %+v", doc.Scenario.Memories["session"].Tiers)
	}
	skill, ok := doc.Scenario.Skills["research"]
	if !ok || skill.Workflow == nil || len(skill.Workflow.Nodes) != 1 {
		t.Fatalf("unexpected skill export: %+v", doc.Scenario.Skills)
	}
	data, err := Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "research") {
		t.Fatalf("expected skill in yaml: %s", data)
	}
}
