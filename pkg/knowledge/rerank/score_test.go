package rerank_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/knowledge/rerank"
)

func TestScoreRerankerPrefersLexicalOverlap(t *testing.T) {
	r := rerank.NewScoreReranker()
	results, err := r.Rerank(context.Background(), "payment refund", []knowledge.SearchResult{
		{Document: knowledge.Document{ID: "1", Content: "shipping policy"}, Score: 0.55},
		{Document: knowledge.Document{ID: "2", Content: "refund payment steps"}, Score: 0.5},
	})
	if err != nil {
		t.Fatalf("rerank failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Document.ID != "2" {
		t.Fatalf("expected refund doc first, got %q", results[0].Document.ID)
	}
}
