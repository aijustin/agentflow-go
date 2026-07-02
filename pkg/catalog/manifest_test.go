package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/catalog"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestLoadToolManifestRejectsInvalidYAML(t *testing.T) {
	if _, err := catalog.LoadToolManifest([]byte(":\n- bad")); err == nil {
		t.Fatal("expected yaml error")
	}
}

func TestLoadSkillManifestRejectsMissingName(t *testing.T) {
	_, err := catalog.LoadSkillManifest([]byte(`
apiVersion: agentflow.dev/v1
kind: Skill
metadata:
  name: ""
spec:
  description: Demo
`))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadToolManifestFileMissingPath(t *testing.T) {
	if _, err := catalog.LoadToolManifestFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestValidateToolManifestRejectsUnsupportedApproval(t *testing.T) {
	err := catalog.ValidateToolManifest(core.Tool{Name: "x", Type: "t", Approval: "bogus"})
	if err == nil {
		t.Fatal("expected unsupported approval error")
	}
}

func TestValidateSkillManifestRejectsEmptyFragment(t *testing.T) {
	err := catalog.ValidateSkillManifest(core.Skill{
		Name:            "demo",
		PromptFragments: []core.PromptFragment{{Name: "system"}},
	})
	if err == nil {
		t.Fatal("expected empty fragment error")
	}
}

func TestLoadSkillManifestFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.skill.yaml")
	content := []byte(`apiVersion: agentflow.dev/v1
kind: Skill
metadata:
  name: demo
  version: "1.0.0"
spec:
  description: Demo skill
  prompt_fragments:
    - name: system
      content: Hello
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	skill, err := catalog.LoadSkillManifestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "demo" || skill.Version != "1.0.0" {
		t.Fatalf("unexpected skill: %+v", skill)
	}
	if len(skill.PromptFragments) != 1 || skill.PromptFragments[0].Content != "Hello" {
		t.Fatalf("unexpected prompt fragments: %+v", skill.PromptFragments)
	}
}

func TestLoadSkillManifestSnakeCaseFields(t *testing.T) {
	skill, err := catalog.LoadSkillManifest([]byte(`apiVersion: agentflow.dev/v1
kind: Skill
metadata:
  name: pos-helper
  version: "2.0.0"
spec:
  description: POS helper skill
  compatible_agents:
    - pos-agent
  prompt_fragments:
    - name: system
      content: Be concise.
  tool_policies:
    - tool: echo
      approval: never
      side_effect: read
      rate_cap: 10
`))
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "pos-helper" || skill.Version != "2.0.0" {
		t.Fatalf("unexpected skill identity: %+v", skill)
	}
	if len(skill.CompatibleAgents) != 1 || skill.CompatibleAgents[0] != "pos-agent" {
		t.Fatalf("unexpected compatible_agents: %+v", skill.CompatibleAgents)
	}
	if len(skill.PromptFragments) != 1 || skill.PromptFragments[0].Content != "Be concise." {
		t.Fatalf("unexpected prompt_fragments: %+v", skill.PromptFragments)
	}
	if len(skill.ToolPolicies) != 1 || skill.ToolPolicies[0].Tool != "echo" || skill.ToolPolicies[0].RateCap != 10 {
		t.Fatalf("unexpected tool_policies: %+v", skill.ToolPolicies)
	}
}

func TestLoadToolManifestSnakeCaseFields(t *testing.T) {
	tool, err := catalog.LoadToolManifest([]byte(`apiVersion: agentflow.dev/v1
kind: Tool
metadata:
  name: echo
spec:
  type: builtin.echo
  description: Echo input
  input_schema:
    type: object
    properties:
      query:
        type: string
  side_effect: read
  approval: never
  rate_cap: 5
`))
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "echo" || tool.Type != "builtin.echo" {
		t.Fatalf("unexpected tool identity: %+v", tool)
	}
	if tool.SideEffect != core.SideEffectRead || tool.Approval != core.ApprovalNever || tool.RateCap != 5 {
		t.Fatalf("unexpected tool policy fields: %+v", tool)
	}
	if len(tool.InputSchema) == 0 {
		t.Fatal("expected input_schema to be loaded")
	}
}
