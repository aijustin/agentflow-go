package yaml

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestValidateRejectsMissingScenarioName(t *testing.T) {
	if err := Validate(core.Scenario{}); err == nil {
		t.Fatal("expected missing name error")
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
