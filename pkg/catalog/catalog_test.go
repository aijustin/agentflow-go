package catalog

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestFromScenario(t *testing.T) {
	cat := FromScenario(core.Scenario{
		Tools:  map[string]core.Tool{"echo": {Type: "builtin.echo"}},
		Skills: map[string]core.Skill{"research": {Workflow: &core.Workflow{}}},
		Agents: map[string]core.Agent{"assistant": {Tools: []string{"echo"}, Skills: []string{"research"}}},
	})
	if len(cat.Tools) != 1 || cat.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", cat.Tools)
	}
	if !cat.Skills[0].HasWorkflow {
		t.Fatal("expected skill workflow flag")
	}
	if cat.Agents[0].Name != "assistant" {
		t.Fatalf("unexpected agents: %+v", cat.Agents)
	}
}
