package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/knowledge"
	"github.com/aijustin/agentflow-go/pkg/mcp"
)

func TestMCPRootConstructors(t *testing.T) {
	client := &rootMCPClient{}
	executor, err := NewMCPToolExecutor(client, "search")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), core.ToolCall{Tool: "docs.search", Input: json.RawMessage(`{"query":"hello"}`)}); err != nil {
		t.Fatal(err)
	}
	if client.called.Name != "search" {
		t.Fatalf("unexpected call: %+v", client.called)
	}
	if _, err := NewMCPHTTPClient("", nil); err == nil {
		t.Fatal("expected endpoint validation error")
	}
}

func TestKnowledgeIngestionRootConstructors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader, err := NewFileKnowledgeLoader(FileKnowledgeLoaderConfig{Paths: []string{path}, Namespace: "tenant-a/docs"})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := &rootVectorStore{}
	indexer, err := NewKnowledgeIndexer(KnowledgeIndexerConfig{Embedder: rootEmbedder{}, Store: store, Profile: "embed"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := indexer.Index(context.Background(), docs)
	if err != nil {
		t.Fatal(err)
	}
	if result.Documents != 1 || result.Chunks != 1 {
		t.Fatalf("unexpected index result: %+v", result)
	}
}

func TestHTTPKnowledgeLoaderRootConstructor(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("remote doc"))
	}))
	defer server.Close()
	loader, err := NewHTTPKnowledgeLoader(HTTPKnowledgeLoaderConfig{URLs: []string{server.URL}, Namespace: "tenant-a/web", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Content != "remote doc" || docs[0].Namespace != "tenant-a/web" {
		t.Fatalf("unexpected docs: %+v", docs)
	}
}

func TestRetrieverRootConstructor(t *testing.T) {
	retriever, err := NewRetrieverTool(RetrieverToolConfig{Embedder: rootEmbedder{}, Store: &rootVectorStore{}, Profile: "embed"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := retriever.Execute(context.Background(), core.ToolCall{Tool: "retrieve", Input: json.RawMessage(`{"query":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tool != "retrieve" || len(result.Output) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

type rootMCPClient struct{ called mcp.CallToolRequest }

func (c *rootMCPClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (c *rootMCPClient) CallTool(_ context.Context, req mcp.CallToolRequest) (mcp.CallToolResult, error) {
	c.called = req
	return mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: "ok"}}}, nil
}

type rootEmbedder struct{}

func (rootEmbedder) Embed(context.Context, string, []string) ([][]float32, error) {
	return [][]float32{{0.1, 0.2}}, nil
}

type rootVectorStore struct{}

func (s *rootVectorStore) Upsert(context.Context, []knowledge.DocumentEmbedding) error { return nil }
func (s *rootVectorStore) Query(context.Context, knowledge.Query) ([]knowledge.SearchResult, error) {
	return []knowledge.SearchResult{{Document: knowledge.Document{ID: "doc-1", Content: "hello"}, Score: 0.9}}, nil
}
func (s *rootVectorStore) Delete(context.Context, knowledge.DeleteRequest) error {
	return errors.New("not implemented")
}
