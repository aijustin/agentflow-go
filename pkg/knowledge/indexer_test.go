package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestIndexerChunksEmbedsAndUpsertsDocuments(t *testing.T) {
	embedder := &recordingEmbedder{vectors: [][][]float32{{{0.1}, {0.2}}}}
	store := &recordingStore{}
	splitter, err := NewTextSplitter(TextSplitterConfig{MaxRunes: 5})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := NewIndexer(IndexerConfig{Embedder: embedder, Store: store, Profile: "embed", Namespace: "tenant-a/docs", BatchSize: 2, Chunker: splitter})
	if err != nil {
		t.Fatal(err)
	}
	result, err := indexer.Index(context.Background(), []Document{{ID: "doc-1", Content: "abcdefghij", Metadata: map[string]string{"source": "guide"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Documents != 1 || result.Chunks != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if embedder.profile != "embed" || len(embedder.inputs) != 1 || len(embedder.inputs[0]) != 2 || embedder.inputs[0][0] != "abcde" || embedder.inputs[0][1] != "fghij" {
		t.Fatalf("unexpected embed calls: %+v", embedder)
	}
	if len(store.documents) != 2 || store.documents[0].Document.Namespace != "tenant-a/docs" || store.documents[1].Document.ID != "doc-1#chunk-000002" || len(store.documents[1].Vector) != 1 {
		t.Fatalf("unexpected upsert documents: %+v", store.documents)
	}
}

func TestIndexerFailsWhenEmbeddingCountDoesNotMatchChunks(t *testing.T) {
	embedder := &recordingEmbedder{vectors: [][][]float32{{{0.1}}}}
	store := &recordingStore{}
	indexer, err := NewIndexer(IndexerConfig{Embedder: embedder, Store: store, Profile: "embed", BatchSize: 2, Chunker: mustSplitter(t, TextSplitterConfig{MaxRunes: 5})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = indexer.Index(context.Background(), []Document{{ID: "doc-1", Content: "abcdefghij"}})
	if err == nil {
		t.Fatal("expected embedding count mismatch error")
	}
	if len(store.documents) != 0 {
		t.Fatalf("store should not be called on mismatch: %+v", store.documents)
	}
}

type recordingEmbedder struct {
	profile string
	inputs  [][]string
	vectors [][][]float32
	err     error
}

func (e *recordingEmbedder) Supports(string, llm.Capability) bool { return true }

func (e *recordingEmbedder) Embed(_ context.Context, profile string, input []string) ([][]float32, error) {
	e.profile = profile
	e.inputs = append(e.inputs, append([]string(nil), input...))
	if e.err != nil {
		return nil, e.err
	}
	if len(e.vectors) == 0 {
		return nil, errors.New("no embedding queued")
	}
	vectors := e.vectors[0]
	e.vectors = e.vectors[1:]
	return vectors, nil
}

type recordingStore struct {
	documents []DocumentEmbedding
}

func (s *recordingStore) Upsert(_ context.Context, documents []DocumentEmbedding) error {
	s.documents = append(s.documents, documents...)
	return nil
}

func (s *recordingStore) Query(context.Context, Query) ([]SearchResult, error) { return nil, nil }
func (s *recordingStore) Delete(context.Context, DeleteRequest) error          { return nil }

func mustSplitter(t *testing.T, config TextSplitterConfig) Chunker {
	t.Helper()
	splitter, err := NewTextSplitter(config)
	if err != nil {
		t.Fatal(err)
	}
	return splitter
}
