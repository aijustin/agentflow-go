package http

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestLoaderFetchesURLs(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path != "/docs/guide" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Guide"))
	}))
	defer server.Close()

	loader, err := NewLoader(Config{URLs: []string{server.URL + "/docs/guide"}, Namespace: "tenant-a/docs", Metadata: map[string]string{"collection": "handbook"}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %+v", docs)
	}
	doc := docs[0]
	if doc.ID != server.URL+"/docs/guide" || doc.Content != "# Guide" || doc.Namespace != "tenant-a/docs" {
		t.Fatalf("unexpected doc: %+v", doc)
	}
	if doc.Metadata["collection"] != "handbook" || doc.Metadata["url"] != server.URL+"/docs/guide" || doc.Metadata["content_type"] != "text/markdown" || doc.Metadata["source"] != server.URL+"/docs/guide" {
		t.Fatalf("unexpected metadata: %+v", doc.Metadata)
	}
}

func TestLoaderRejectsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Error(w, "nope", nethttp.StatusBadGateway)
	}))
	defer server.Close()
	loader, err := NewLoader(Config{URLs: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("expected non-success status error")
	}
}

func TestLoaderRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("too large"))
	}))
	defer server.Close()
	loader, err := NewLoader(Config{URLs: []string{server.URL}, MaxBytes: 3, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("expected oversized response error")
	}
}
