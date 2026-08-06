package agentflow_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/httpx"

	"github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type stubEmbedder struct{}

func (stubEmbedder) Embed(context.Context, string, []string) ([][]float32, error) {
	return [][]float32{{0.1, 0.2}}, nil
}

type stubVectorStore struct{}

func (stubVectorStore) Upsert(context.Context, []knowledge.DocumentEmbedding) error { return nil }
func (stubVectorStore) Query(context.Context, knowledge.Query) ([]knowledge.SearchResult, error) {
	return nil, nil
}
func (stubVectorStore) Delete(context.Context, knowledge.DeleteRequest) error { return nil }

func TestKnowledgeWiringOptionsBindsCollection(t *testing.T) {
	scenario := core.Scenario{
		Name: "rag",
		LLMs: map[string]core.LLMProfileRef{
			"embed": {Provider: "mock", Model: "embed", Capabilities: []string{"embed"}},
		},
		Knowledge: core.KnowledgeConfig{
			Collections: []core.KnowledgeCollection{{
				Name:         "docs",
				Namespace:    "docs",
				Tool:         "knowledge.retrieve",
				EmbedProfile: "embed",
				TenantScoped: true,
			}},
		},
		Tools: map[string]core.Tool{
			"knowledge.retrieve": {Name: "knowledge.retrieve", Type: "knowledge.retriever"},
		},
	}
	opts, err := httpx.KnowledgeWiringOptions(scenario, httpx.KnowledgeRegistry{
		Embedder: stubEmbedder{},
		Store:    stubVectorStore{},
	})
	if err != nil {
		t.Fatalf("KnowledgeWiringOptions failed: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
	if err := agentflow.ValidateWiring(scenario, append(opts, agentflow.WithLLMGateway(stubGateway{}))...); err != nil {
		t.Fatalf("ValidateWiring failed: %v", err)
	}
}

func TestKnowledgeWiringOptionsRejectsMissingRegistry(t *testing.T) {
	scenario := core.Scenario{
		Name: "rag",
		Knowledge: core.KnowledgeConfig{
			Collections: []core.KnowledgeCollection{{Name: "docs", Namespace: "docs"}},
		},
	}
	if _, err := httpx.KnowledgeWiringOptions(scenario, httpx.KnowledgeRegistry{}); err == nil {
		t.Fatal("expected embedder/store required error")
	}
}

func TestKnowledgeWiringOptionsRejectsMissingTool(t *testing.T) {
	scenario := core.Scenario{
		Name: "rag",
		LLMs: map[string]core.LLMProfileRef{
			"embed": {Provider: "mock", Model: "embed", Capabilities: []string{"embed"}},
		},
		Knowledge: core.KnowledgeConfig{
			Collections: []core.KnowledgeCollection{{
				Name: "docs", Namespace: "docs", Tool: "missing.tool", EmbedProfile: "embed",
			}},
		},
	}
	_, err := httpx.KnowledgeWiringOptions(scenario, httpx.KnowledgeRegistry{
		Embedder: stubEmbedder{},
		Store:    stubVectorStore{},
	})
	if err == nil {
		t.Fatal("expected missing tool error")
	}
}

func TestKnowledgeWiringOptionsHybridSearchMode(t *testing.T) {
	scenario := core.Scenario{
		Name: "rag",
		LLMs: map[string]core.LLMProfileRef{
			"embed": {Provider: "mock", Model: "embed", Capabilities: []string{"embed"}},
		},
		Knowledge: core.KnowledgeConfig{
			Collections: []core.KnowledgeCollection{{
				Name: "docs", Namespace: "docs", Tool: "knowledge.retrieve",
				EmbedProfile: "embed", SearchMode: "hybrid",
			}},
		},
		Tools: map[string]core.Tool{
			"knowledge.retrieve": {Name: "knowledge.retrieve", Type: "knowledge.retriever"},
		},
	}
	opts, err := httpx.KnowledgeWiringOptions(scenario, httpx.KnowledgeRegistry{
		Embedder: stubEmbedder{},
		Store:    stubVectorStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestKnowledgeWiringOptionsDefaultToolName(t *testing.T) {
	scenario := core.Scenario{
		Name: "rag-default-tool",
		LLMs: map[string]core.LLMProfileRef{
			"embed": {Provider: "mock", Model: "embed", Capabilities: []string{"embed"}},
		},
		Knowledge: core.KnowledgeConfig{
			Collections: []core.KnowledgeCollection{{
				Name: "docs", Namespace: "docs", EmbedProfile: "embed",
			}},
		},
		Tools: map[string]core.Tool{
			"knowledge.docs": {Name: "knowledge.docs", Type: "knowledge.retriever"},
		},
	}
	opts, err := httpx.KnowledgeWiringOptions(scenario, httpx.KnowledgeRegistry{
		Embedder: stubEmbedder{},
		Store:    stubVectorStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestMCPWiringOptionsRequiresMetadata(t *testing.T) {
	scenario := core.Scenario{
		Name: "mcp",
		MCP: core.MCPConfig{
			Servers: []core.MCPServer{{Name: "docs", Transport: "http", URL: "http://127.0.0.1:1/mcp"}},
		},
		Tools: map[string]core.Tool{
			"docs.search": {Name: "docs.search", Type: "mcp.tool"},
		},
	}
	_, err := httpx.MCPWiringOptions(context.Background(), scenario, httpx.MCPRegistry{})
	if err == nil {
		t.Fatal("expected metadata error")
	}
}

type stubGateway struct{}

func (stubGateway) Supports(string, llm.Capability) bool { return true }
func (stubGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (stubGateway) ChatStream(context.Context, string, llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return nil, nil
}
func (stubGateway) ChatWithTools(context.Context, string, llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	return llm.ToolCallResponse{}, nil
}
func (stubGateway) StructuredChat(context.Context, string, []byte, llm.ChatRequest) ([]byte, error) {
	return nil, nil
}
