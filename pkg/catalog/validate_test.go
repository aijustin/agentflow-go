package catalog

import (
	"testing"

	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestValidateToolManifest(t *testing.T) {
	tool := core.Tool{
		Name:       "echo",
		Type:       "builtin.echo",
		SideEffect: core.SideEffectNone,
		Approval:   core.ApprovalNever,
	}
	if err := ValidateToolManifest(tool); err != nil {
		t.Fatalf("expected valid tool: %v", err)
	}
}

func TestValidateToolManifestRejectsDangerousWithoutApproval(t *testing.T) {
	tool := core.Tool{
		Name:       "delete_all",
		Type:       "custom.delete",
		SideEffect: core.SideEffectDangerous,
		Approval:   core.ApprovalNever,
	}
	if err := ValidateToolManifest(tool); err == nil {
		t.Fatal("expected dangerous tool to require approval")
	}
}

func TestValidateSkillManifest(t *testing.T) {
	skill := core.Skill{
		Name:        "code_review",
		Version:     "1.0.0",
		Description: "Review code changes",
		PromptFragments: []core.PromptFragment{
			{Name: "system", Content: "You review code carefully."},
		},
	}
	if err := ValidateSkillManifest(skill); err != nil {
		t.Fatalf("expected valid skill: %v", err)
	}
}

func TestLoadToolManifest(t *testing.T) {
	tool, err := LoadToolManifest([]byte(`
apiVersion: agentflow.dev/v1
kind: Tool
metadata:
  name: echo
  version: "1.0.0"
spec:
  type: builtin.echo
  description: Echo input
  side_effect: none
  approval: never
`))
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "echo" || tool.Type != "builtin.echo" {
		t.Fatalf("unexpected tool: %+v", tool)
	}
}

func TestValidateToolManifestRejectsInvalidFields(t *testing.T) {
	cases := []core.Tool{
		{Name: "", Type: "builtin.echo"},
		{Name: "echo", Type: ""},
		{Name: "echo", Type: "builtin.echo", Approval: core.ApprovalPolicy("maybe")},
		{Name: "echo", Type: "builtin.echo", SideEffect: core.SideEffectLevel("explosive")},
		{Name: "echo", Type: "builtin.echo", RateCap: -1},
		{Name: "echo", Type: "builtin.echo", InputSchema: json.RawMessage(`{`)},
	}
	for _, tool := range cases {
		if err := ValidateToolManifest(tool); err == nil {
			t.Fatalf("expected validation error for %+v", tool)
		}
	}
}

func TestValidateSkillManifestRejectsInvalidFields(t *testing.T) {
	cases := []core.Skill{
		{Name: ""},
		{Name: "skill", Version: "not-a-version"},
		{Name: "skill", PromptFragments: []core.PromptFragment{{Name: "x", Content: ""}}},
		{Name: "skill", ToolPolicies: []core.SkillToolPolicy{{Tool: ""}}},
		{Name: "skill", ToolPolicies: []core.SkillToolPolicy{{Tool: "echo", Approval: core.ApprovalPolicy("maybe")}}},
		{Name: "skill", ToolPolicies: []core.SkillToolPolicy{{Tool: "echo", RateCap: -1}}},
	}
	for _, skill := range cases {
		if err := ValidateSkillManifest(skill); err == nil {
			t.Fatalf("expected validation error for %+v", skill)
		}
	}
}
