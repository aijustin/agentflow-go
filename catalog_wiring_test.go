package agentflow_test

import (
	"path/filepath"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestCatalogManifestWrappers(t *testing.T) {
	toolPath := filepath.Join("examples", "catalog", "tools", "echo.tool.yaml")
	tool, err := agentflow.LoadToolManifestFile(toolPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateToolManifest(tool); err != nil {
		t.Fatal(err)
	}
	rawTool, err := agentflow.LoadToolManifest([]byte("apiVersion: agentflow.dev/v1\nkind: Tool\nmetadata:\n  name: echo\n  version: \"1.0.0\"\nspec:\n  type: builtin.echo\n  description: Echo\n  side_effect: none\n  approval: never\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rawTool.Name != "echo" {
		t.Fatalf("unexpected tool: %+v", rawTool)
	}

	skillPath := filepath.Join("examples", "catalog", "skills", "code_review.skill.yaml")
	skill, err := agentflow.LoadSkillManifestFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateSkillManifest(skill); err != nil {
		t.Fatal(err)
	}
	rawSkill, err := agentflow.LoadSkillManifest([]byte("apiVersion: agentflow.dev/v1\nkind: Skill\nmetadata:\n  name: demo\n  version: \"1.0.0\"\nspec:\n  description: Demo skill\n  prompt_fragments:\n    - name: system\n      content: Hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rawSkill.Name != "demo" {
		t.Fatalf("unexpected skill: %+v", rawSkill)
	}
	if skill.Name == "" {
		t.Fatal("expected loaded skill name")
	}
	_ = core.Tool{}
}
