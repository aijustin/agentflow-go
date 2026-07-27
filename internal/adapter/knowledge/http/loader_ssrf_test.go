package http

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoaderBlocksMetadataEndpoint(t *testing.T) {
	loader, err := NewLoader(Config{URLs: []string{"http://169.254.169.254/latest/meta-data/"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("expected ingestion from the metadata endpoint to be blocked")
	}
}

// A source that redirects into a private range must be refused mid-chain.
func TestLoaderBlocksRedirectIntoPrivateRange(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, "http://10.0.0.5/internal", nethttp.StatusFound)
	}))
	defer server.Close()

	loader, err := NewLoader(Config{URLs: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = loader.Load(context.Background())
	if err == nil {
		t.Fatal("expected redirect into a private range to be blocked")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}
}

func TestLoaderBlockLoopbackRejectsLocalSource(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("local"))
	}))
	defer server.Close()

	loader, err := NewLoader(Config{URLs: []string{server.URL}, BlockLoopback: true, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("expected loopback source to be blocked when BlockLoopback is set")
	}
}

func TestLoaderAllowsLoopbackByDefault(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("doc body"))
	}))
	defer server.Close()

	loader, err := NewLoader(Config{URLs: []string{server.URL}, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	documents, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("expected loopback source to load by default, got %v", err)
	}
	if len(documents) != 1 || documents[0].Content != "doc body" {
		t.Fatalf("unexpected documents %+v", documents)
	}
}
