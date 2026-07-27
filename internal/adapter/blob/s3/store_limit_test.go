package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// The bucket is a separate trust domain: an object larger than expected must
// be refused rather than buffered whole into the agent's heap.
func TestStoreGetRejectsObjectOverMaxBytes(t *testing.T) {
	body := strings.Repeat("a", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		MaxObjectBytes:  1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := runstate.NewBlobRef("", []byte(body))
	ref.ID = ref.Sha256
	_, err = store.Get(context.Background(), ref)
	if err == nil {
		t.Fatal("expected oversized object to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds max object bytes") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

// An object exactly at the limit is still valid; the cap must not be off by one.
func TestStoreGetAcceptsObjectAtMaxBytes(t *testing.T) {
	body := strings.Repeat("a", 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		MaxObjectBytes:  1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := runstate.NewBlobRef("", []byte(body))
	ref.ID = ref.Sha256
	data, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("expected object at the limit to be readable, got %v", err)
	}
	if len(data) != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", len(data))
	}
}

func TestNewStoreDefaultsMaxObjectBytes(t *testing.T) {
	store, err := NewStore(Config{
		Endpoint:        "https://s3.example.com",
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.maxObjectBytes != DefaultMaxObjectBytes {
		t.Fatalf("expected default max object bytes %d, got %d", DefaultMaxObjectBytes, store.maxObjectBytes)
	}
}
