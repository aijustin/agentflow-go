package agentflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

func TestTenantKnowledgeNamespace(t *testing.T) {
	if got := tenantKnowledgeNamespace("docs", "tenant-a"); got != "tenant-a/docs" {
		t.Fatalf("got %q", got)
	}
	if got := tenantKnowledgeNamespace("", "tenant-a"); got != "tenant-a" {
		t.Fatalf("empty base: got %q", got)
	}
	if got := tenantKnowledgeNamespace("docs", ""); got != "docs" {
		t.Fatalf("empty tenant: got %q", got)
	}
}

func TestFirstEmbedProfile(t *testing.T) {
	if got := firstEmbedProfile(map[string]core.LLMProfileRef{
		"chat":  {Provider: "mock", Model: "chat"},
		"embed": {Provider: "mock", Model: "embed", Capabilities: []string{"embed"}},
	}); got != "embed" {
		t.Fatalf("got %q", got)
	}
	if got := firstEmbedProfile(map[string]core.LLMProfileRef{
		"only": {Provider: "mock", Model: "only"},
	}); got != "only" {
		t.Fatalf("fallback profile: got %q", got)
	}
}

func TestTenantScopedRetrieverInjectsNamespace(t *testing.T) {
	inner, err := NewRetrieverTool(RetrieverToolConfig{
		Embedder: rootEmbedder{},
		Store:    &capturingVectorStore{},
		Profile:  "embed",
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &tenantScopedRetriever{
		inner:        inner,
		tenantScoped: true,
		namespace:    "docs",
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		Scope: identity.Scope{TenantID: "tenant-a"},
	})
	_, err = wrapped.Execute(ctx, core.ToolCall{
		Tool:  "retrieve",
		Input: json.RawMessage(`{"query":"billing"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
}

type capturingVectorStore struct {
	lastQuery knowledge.Query
}

func (s *capturingVectorStore) Upsert(context.Context, []knowledge.DocumentEmbedding) error { return nil }
func (s *capturingVectorStore) Query(_ context.Context, q knowledge.Query) ([]knowledge.SearchResult, error) {
	s.lastQuery = q
	return nil, nil
}
func (s *capturingVectorStore) Delete(context.Context, knowledge.DeleteRequest) error { return nil }
