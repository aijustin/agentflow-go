package retriever

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestToolEmbedsQueryAndReturnsMatches(t *testing.T) {
	embedder := &fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}}
	store := &fakeStore{results: []knowledge.SearchResult{{Document: knowledge.Document{ID: "doc-1#chunk-000001", Content: "hello", Metadata: map[string]string{"source": "guide", "parent_id": "doc-1", "chunk_index": "0", "chunk_start": "10", "chunk_end": "20", "url": "https://example.test/doc-1"}}, Score: 0.9}}}
	tool, err := NewTool(Config{Embedder: embedder, Store: store, Profile: "embed", Namespace: "tenant-a", DefaultLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), core.ToolCall{Tool: "retrieve", Input: json.RawMessage(`{"query":"hello","limit":2,"filter":{"source":"guide"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if embedder.profile != "embed" || len(embedder.input) != 1 || embedder.input[0] != "hello" {
		t.Fatalf("unexpected embedding call: %+v", embedder)
	}
	if store.query.Namespace != "tenant-a" || store.query.Limit != 2 || len(store.query.Vector) != 2 {
		t.Fatalf("unexpected vector query: %+v", store.query)
	}
	if store.query.Filter["source"] != "guide" {
		t.Fatalf("filter not passed through: %+v", store.query.Filter)
	}
	var out Response
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "doc-1#chunk-000001" || out.Results[0].Score != 0.9 {
		t.Fatalf("unexpected response: %+v", out)
	}
	if out.Results[0].Citation.ParentID != "doc-1" || out.Results[0].Citation.Source != "guide" || out.Results[0].Citation.URL != "https://example.test/doc-1" || out.Results[0].Citation.ChunkStart != "10" {
		t.Fatalf("unexpected citation: %+v", out.Results[0].Citation)
	}
}

func TestToolRequiresQuery(t *testing.T) {
	tool, err := NewTool(Config{Embedder: &fakeEmbedder{}, Store: &fakeStore{}, Profile: "embed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), core.ToolCall{Tool: "retrieve", Input: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("expected missing query error")
	}
}

func TestToolUsesHybridSearchAndReranker(t *testing.T) {
	store := &fakeStore{results: []knowledge.SearchResult{
		{Document: knowledge.Document{ID: "low", Content: "low"}, Score: 0.2},
		{Document: knowledge.Document{ID: "high", Content: "high"}, Score: 0.9},
	}}
	reranker := &fakeReranker{}
	tool, err := NewTool(Config{
		Embedder:            &fakeEmbedder{vectors: [][]float32{{0.1, 0.2}}},
		Store:               store,
		Profile:             "embed",
		DefaultLimit:        1,
		SearchMode:          knowledge.SearchModeHybrid,
		CandidateMultiplier: 2,
		Reranker:            reranker,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), core.ToolCall{Tool: "retrieve", Input: json.RawMessage(`{"query":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !store.hybridCalled || store.query.Text != "hello" || store.query.Mode != knowledge.SearchModeHybrid || store.query.Limit != 2 {
		t.Fatalf("expected hybrid search with expanded candidate limit, got called=%v query=%+v", store.hybridCalled, store.query)
	}
	if reranker.query != "hello" || len(reranker.input) != 2 {
		t.Fatalf("unexpected reranker input: %+v", reranker)
	}
	var out Response
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "high" {
		t.Fatalf("expected reranked top result, got %+v", out.Results)
	}
}

type fakeEmbedder struct {
	profile string
	input   []string
	vectors [][]float32
}

func (f *fakeEmbedder) Supports(string, llm.Capability) bool { return true }

func (f *fakeEmbedder) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (f *fakeEmbedder) Embed(_ context.Context, profile string, input []string) ([][]float32, error) {
	f.profile = profile
	f.input = input
	return f.vectors, nil
}

type fakeStore struct {
	query        knowledge.Query
	results      []knowledge.SearchResult
	hybridCalled bool
}

func (f *fakeStore) Upsert(context.Context, []knowledge.DocumentEmbedding) error { return nil }

func (f *fakeStore) Query(_ context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	f.query = query
	return f.results, nil
}

func (f *fakeStore) HybridQuery(_ context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	f.hybridCalled = true
	f.query = query
	return f.results, nil
}

func (f *fakeStore) Delete(context.Context, knowledge.DeleteRequest) error { return nil }

type fakeReranker struct {
	query string
	input []knowledge.SearchResult
}

func (f *fakeReranker) Rerank(_ context.Context, query string, results []knowledge.SearchResult) ([]knowledge.SearchResult, error) {
	f.query = query
	f.input = append([]knowledge.SearchResult(nil), results...)
	return []knowledge.SearchResult{results[1], results[0]}, nil
}
