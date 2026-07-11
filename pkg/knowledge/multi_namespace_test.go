package knowledge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

type stubMultiStore struct {
	byNamespace map[string][]knowledge.SearchResult
	errs        map[string]error
	calls       []string
}

func (s *stubMultiStore) Upsert(context.Context, []knowledge.DocumentEmbedding) error {
	return nil
}

func (s *stubMultiStore) Delete(context.Context, knowledge.DeleteRequest) error { return nil }

func (s *stubMultiStore) Query(_ context.Context, query knowledge.Query) ([]knowledge.SearchResult, error) {
	s.calls = append(s.calls, query.Namespace)
	if err, ok := s.errs[query.Namespace]; ok {
		return nil, err
	}
	results := s.byNamespace[query.Namespace]
	out := make([]knowledge.SearchResult, len(results))
	copy(out, results)
	return out, nil
}

func TestMultiNamespaceRetrieverGlobalRank(t *testing.T) {
	store := &stubMultiStore{
		byNamespace: map[string][]knowledge.SearchResult{
			"kb-a": {
				{Document: knowledge.Document{ID: "a1", Content: "a1"}, Score: 0.5},
				{Document: knowledge.Document{ID: "a2", Content: "a2"}, Score: 0.9},
			},
			"kb-b": {
				{Document: knowledge.Document{ID: "b1", Content: "b1"}, Score: 0.8},
			},
		},
	}
	retriever, err := knowledge.NewMultiNamespaceRetriever(store, []string{"kb-a", "kb-b"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Query(context.Background(), knowledge.Query{Vector: []float32{0.1}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Document.ID != "a2" || results[1].Document.ID != "b1" {
		t.Fatalf("unexpected global rank order: %+v", results)
	}
	if results[0].Document.Metadata["namespace"] != "kb-a" || results[0].Document.Metadata["kb_id"] != "kb-a" {
		t.Fatalf("expected namespace metadata on result: %+v", results[0].Document.Metadata)
	}
	if results[1].Document.Metadata["namespace"] != "kb-b" {
		t.Fatalf("expected kb-b namespace metadata: %+v", results[1].Document.Metadata)
	}
}

func TestMultiNamespaceRetrieverBalanced(t *testing.T) {
	store := &stubMultiStore{
		byNamespace: map[string][]knowledge.SearchResult{
			"kb-a": {
				{Document: knowledge.Document{ID: "a1"}, Score: 0.99},
				{Document: knowledge.Document{ID: "a2"}, Score: 0.98},
			},
			"kb-b": {
				{Document: knowledge.Document{ID: "b1"}, Score: 0.1},
			},
		},
	}
	retriever, err := knowledge.NewMultiNamespaceRetriever(
		store,
		[]string{"kb-a", "kb-b"},
		knowledge.WithMergeStrategy(knowledge.MergeStrategyBalanced),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Query(context.Background(), knowledge.Query{Vector: []float32{0.1}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	ids := []string{results[0].Document.ID, results[1].Document.ID}
	if ids[0] != "a1" || ids[1] != "b1" {
		t.Fatalf("expected balanced interleave a1,b1 got %v", ids)
	}
}

func TestMultiNamespaceRetrieverContinuesOnPartialFailure(t *testing.T) {
	store := &stubMultiStore{
		byNamespace: map[string][]knowledge.SearchResult{
			"kb-ok": {{Document: knowledge.Document{ID: "ok"}, Score: 0.7}},
		},
		errs: map[string]error{
			"kb-bad": errors.New("boom"),
		},
	}
	retriever, err := knowledge.NewMultiNamespaceRetriever(store, []string{"kb-bad", "kb-ok"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Query(context.Background(), knowledge.Query{Vector: []float32{0.1}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Document.ID != "ok" {
		t.Fatalf("expected surviving namespace result, got %+v", results)
	}
}

func TestMultiNamespaceRetrieverAllFail(t *testing.T) {
	store := &stubMultiStore{
		errs: map[string]error{
			"kb-a": errors.New("a failed"),
			"kb-b": errors.New("b failed"),
		},
	}
	retriever, err := knowledge.NewMultiNamespaceRetriever(store, []string{"kb-a", "kb-b"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = retriever.Query(context.Background(), knowledge.Query{Vector: []float32{0.1}})
	if err == nil {
		t.Fatal("expected error when all namespaces fail")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestNewMultiNamespaceRetrieverValidatesInputs(t *testing.T) {
	if _, err := knowledge.NewMultiNamespaceRetriever(nil, []string{"a"}); err == nil {
		t.Fatal("expected nil store error")
	}
	if _, err := knowledge.NewMultiNamespaceRetriever(&stubMultiStore{}, nil); err == nil {
		t.Fatal("expected empty namespaces error")
	}
	if _, err := knowledge.NewMultiNamespaceRetriever(
		&stubMultiStore{},
		[]string{"a"},
		knowledge.WithMergeStrategy(knowledge.MergeStrategy("nope")),
	); err == nil {
		t.Fatal("expected unknown strategy error")
	}
}

func TestMultiNamespaceRetrieverPreservesExistingKbID(t *testing.T) {
	store := &stubMultiStore{
		byNamespace: map[string][]knowledge.SearchResult{
			"ns-1": {{
				Document: knowledge.Document{
					ID:       "doc",
					Metadata: map[string]string{"kb_id": "custom-kb"},
				},
				Score: 1,
			}},
		},
	}
	retriever, err := knowledge.NewMultiNamespaceRetriever(store, []string{"ns-1"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Query(context.Background(), knowledge.Query{Vector: []float32{0.1}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Document.Metadata["kb_id"] != "custom-kb" {
		t.Fatalf("expected existing kb_id preserved, got %+v", results[0].Document.Metadata)
	}
	if results[0].Document.Metadata["namespace"] != "ns-1" {
		t.Fatalf("expected namespace injected, got %+v", results[0].Document.Metadata)
	}
}

func TestMultiNamespaceRetrieverRespectsCancelledContext(t *testing.T) {
	retriever, err := knowledge.NewMultiNamespaceRetriever(&stubMultiStore{}, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retriever.Query(ctx, knowledge.Query{Vector: []float32{0.1}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
