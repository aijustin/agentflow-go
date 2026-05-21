package knowledge_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

func TestMergeRRFCombinesLists(t *testing.T) {
	listA := []knowledge.SearchResult{
		{Document: knowledge.Document{ID: "a", Content: "alpha"}, Score: 0.9},
		{Document: knowledge.Document{ID: "b", Content: "beta"}, Score: 0.8},
	}
	listB := []knowledge.SearchResult{
		{Document: knowledge.Document{ID: "b", Content: "beta"}, Score: 0.95},
		{Document: knowledge.Document{ID: "c", Content: "gamma"}, Score: 0.7},
	}
	merged := knowledge.MergeRRF([][]knowledge.SearchResult{listA, listB}, 60, 3)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged results, got %d", len(merged))
	}
	if merged[0].Document.ID != "b" {
		t.Fatalf("expected top fused doc b, got %q", merged[0].Document.ID)
	}
}
