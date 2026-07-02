package rerank_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/knowledge/rerank"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type llmRankGateway struct {
	content string
}

func (g llmRankGateway) Chat(_ context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: g.content}}, nil
}

func (g llmRankGateway) Supports(string, llm.Capability) bool { return true }

func TestLLMRerankerOrdersByModelResponse(t *testing.T) {
	r := rerank.NewLLMReranker(llmRankGateway{content: `{"ids":["2","1"]}`}, "default")
	results, err := r.Rerank(context.Background(), "payment", []knowledge.SearchResult{
		{Document: knowledge.Document{ID: "1", Content: "shipping"}, Score: 0.9},
		{Document: knowledge.Document{ID: "2", Content: "payment refund"}, Score: 0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Document.ID != "2" {
		t.Fatalf("unexpected order: %+v", results)
	}
}

func TestLLMRerankerFallsBackOnInvalidJSON(t *testing.T) {
	r := rerank.NewLLMReranker(llmRankGateway{content: "not-json"}, "default")
	results, err := r.Rerank(context.Background(), "refund payment", []knowledge.SearchResult{
		{Document: knowledge.Document{ID: "1", Content: "shipping"}, Score: 0.55},
		{Document: knowledge.Document{ID: "2", Content: "refund payment"}, Score: 0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected fallback rerank, got %+v", results)
	}
}
