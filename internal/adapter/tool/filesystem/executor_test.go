package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestExecutorReadsFileInsideAllowedRoot(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "notes", "status.txt")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("service ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor, err := NewExecutor(Config{AllowedRoots: []string{root}, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "fs.read", Input: mustInput(t, Request{Path: filePath})})
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.Path != filePath || out.Size != int64(len("service ok\n")) || out.Content != "service ok\n" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestExecutorRejectsTraversalOutsideAllowedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "allowed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor, err := NewExecutor(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "fs.read", Input: mustInput(t, Request{Path: filepath.Join(root, "..", "secret.txt")})})
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestExecutorRejectsSymlinkOutsideAllowedRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	executor, err := NewExecutor(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "fs.read", Input: mustInput(t, Request{Path: linkPath})})
	if err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestExecutorRejectsOversizedFiles(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "large.txt")
	if err := os.WriteFile(filePath, []byte("too large"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor, err := NewExecutor(Config{AllowedRoots: []string{root}, MaxBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "fs.read", Input: mustInput(t, Request{Path: filePath})})
	if err == nil {
		t.Fatal("expected oversized file rejection")
	}
}

func mustInput(t *testing.T, input Request) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
