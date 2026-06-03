package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestStorePutsAndGetsBlobs(t *testing.T) {
	ctx := context.Background()
	server := newFakeS3(t)
	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		Prefix:          "runs/steps",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(ctx, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" || ref.Sha256 == "" || ref.Size != 5 {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("unexpected blob: %q", got)
	}
	server.assertSawSignedRequests(t)
}

func TestStoreGetMissingBlob(t *testing.T) {
	ctx := context.Background()
	server := newFakeS3(t)
	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := runstate.NewBlobRef("missing", []byte("missing"))
	ref.ID = ref.Sha256
	_, err = store.Get(ctx, ref)
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStoreDetectsChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	t.Cleanup(server.Close)
	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := runstate.NewBlobRef("", []byte("hello"))
	ref.ID = ref.Sha256
	_, err = store.Get(ctx, ref)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestStoreListsAndDeletesBlobs(t *testing.T) {
	ctx := context.Background()
	server := newFakeS3(t)
	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		Prefix:          "runs/steps",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	refA, err := store.Put(ctx, []byte("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	refB, err := store.Put(ctx, []byte("beta"))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %+v", refs)
	}
	if err := store.Delete(ctx, refA); err != nil {
		t.Fatal(err)
	}
	refs, err = store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != refB.ID {
		t.Fatalf("unexpected refs after delete: %+v", refs)
	}
	if err := store.Delete(ctx, refB); err != nil {
		t.Fatal(err)
	}
	refs, err = store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected empty list, got %+v", refs)
	}
}

func TestStoreListPaginates(t *testing.T) {
	ctx := context.Background()
	server := newFakeS3(t)
	server.pageSize = 2
	store, err := NewStore(Config{
		Endpoint:        server.URL,
		Bucket:          "agentflow-blobs",
		Region:          "us-east-1",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	const total = 7
	for i := 0; i < total; i++ {
		if _, err := store.Put(ctx, []byte{byte('a' + i)}); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != total {
		t.Fatalf("expected %d refs across pages, got %d", total, len(refs))
	}
}

func TestNewStoreValidatesConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "missing endpoint", config: Config{Bucket: "agentflow-blobs", Region: "us-east-1", AccessKeyID: "access", SecretAccessKey: "secret"}},
		{name: "invalid endpoint", config: Config{Endpoint: "ftp://example.com", Bucket: "agentflow-blobs", Region: "us-east-1", AccessKeyID: "access", SecretAccessKey: "secret"}},
		{name: "missing bucket", config: Config{Endpoint: "https://example.com", Region: "us-east-1", AccessKeyID: "access", SecretAccessKey: "secret"}},
		{name: "missing region", config: Config{Endpoint: "https://example.com", Bucket: "agentflow-blobs", AccessKeyID: "access", SecretAccessKey: "secret"}},
		{name: "missing access key", config: Config{Endpoint: "https://example.com", Bucket: "agentflow-blobs", Region: "us-east-1", SecretAccessKey: "secret"}},
		{name: "missing secret", config: Config{Endpoint: "https://example.com", Bucket: "agentflow-blobs", Region: "us-east-1", AccessKeyID: "access"}},
		{name: "invalid prefix", config: Config{Endpoint: "https://example.com", Bucket: "agentflow-blobs", Region: "us-east-1", Prefix: "../escape", AccessKeyID: "access", SecretAccessKey: "secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewStore(tt.config); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

type fakeS3 struct {
	*httptest.Server
	mu             sync.Mutex
	objects        map[string][]byte
	signedRequests int
	pageSize       int
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	server := &fakeS3{objects: make(map[string][]byte)}
	server.Server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.Close)
	return server
}

func (s *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		http.Error(w, "missing authorization", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("X-Amz-Date") == "" || r.Header.Get("X-Amz-Content-Sha256") == "" {
		http.Error(w, "missing signing headers", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	s.signedRequests++
	s.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		data := new(bytes.Buffer)
		_, _ = data.ReadFrom(r.Body)
		s.mu.Lock()
		s.objects[r.URL.Path] = append([]byte(nil), data.Bytes()...)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		if r.URL.Query().Get("list-type") == "2" {
			s.writeListResponse(w, r)
			return
		}
		s.mu.Lock()
		data, ok := s.objects[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.objects, r.URL.Path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
	}
}

func (s *fakeS3) writeListResponse(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects))
	for objectPath := range s.objects {
		key := strings.TrimPrefix(objectPath, "/agentflow-blobs/")
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	start := 0
	if token := r.URL.Query().Get("continuation-token"); token != "" {
		if v, err := strconv.Atoi(token); err == nil {
			start = v
		}
	}
	end := len(keys)
	truncated := false
	nextToken := ""
	if s.pageSize > 0 && start+s.pageSize < len(keys) {
		end = start + s.pageSize
		truncated = true
		nextToken = strconv.Itoa(end)
	}
	page := keys[start:end]

	result := listBucketResult{
		Contents:              make([]listObject, len(page)),
		IsTruncated:           truncated,
		NextContinuationToken: nextToken,
	}
	for i, key := range page {
		result.Contents[i] = listObject{Key: key, Size: int64(len(s.objects["/agentflow-blobs/"+key]))}
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(result)
}

func (s *fakeS3) assertSawSignedRequests(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signedRequests != 2 {
		t.Fatalf("expected 2 signed requests, got %d", s.signedRequests)
	}
	for objectPath := range s.objects {
		if !strings.HasPrefix(objectPath, "/agentflow-blobs/runs/steps/") {
			t.Fatalf("unexpected object path %q", objectPath)
		}
	}
}
