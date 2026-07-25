package agentflow

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestResolveComposerLLM(t *testing.T) {
	t.Parallel()

	base := core.Scenario{
		LLMs: map[string]core.LLMProfileRef{
			"chat": {Provider: "mock", Model: "test"},
			"zeta": {Provider: "mock", Model: "test"},
		},
	}
	name, err := resolveComposerLLM(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "chat" {
		t.Fatalf("expected first sorted profile chat, got %q", name)
	}

	name, err = resolveComposerLLM(base, "zeta")
	if err != nil {
		t.Fatal(err)
	}
	if name != "zeta" {
		t.Fatalf("expected requested zeta, got %q", name)
	}

	if _, err := resolveComposerLLM(base, "missing"); err == nil {
		t.Fatal("expected missing profile error")
	}
	if _, err := resolveComposerLLM(core.Scenario{}, ""); err == nil {
		t.Fatal("expected empty llm map error")
	}

	withDefault := core.Scenario{
		LLMs: map[string]core.LLMProfileRef{
			"default": {Provider: "mock", Model: "test"},
			"chat":    {Provider: "mock", Model: "test"},
		},
	}
	name, err = resolveComposerLLM(withDefault, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "default" {
		t.Fatalf("expected default profile preference, got %q", name)
	}
}
