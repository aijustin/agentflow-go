package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderReadsFilesRecursively(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "alpha")
	writeFile(t, filepath.Join(dir, "nested", "b.txt"), "bravo")
	loader, err := NewLoader(Config{Paths: []string{dir}, Namespace: "tenant-a/docs", Metadata: map[string]string{"collection": "handbook"}})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %+v", docs)
	}
	if docs[0].ID != "a.md" || docs[0].Content != "alpha" || docs[0].Namespace != "tenant-a/docs" || docs[0].Metadata["collection"] != "handbook" || docs[0].Metadata["extension"] != ".md" {
		t.Fatalf("unexpected first doc: %+v", docs[0])
	}
	if docs[1].ID != "nested/b.txt" || docs[1].Content != "bravo" || docs[1].Metadata["path"] == "" {
		t.Fatalf("unexpected second doc: %+v", docs[1])
	}
}

func TestLoaderRejectsOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	writeFile(t, path, "too large")
	loader, err := NewLoader(Config{Paths: []string{path}, MaxBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("expected oversized file error")
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
