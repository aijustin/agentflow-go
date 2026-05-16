package agentflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestFilesystemToolRootConstructor(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "status.txt")
	if err := os.WriteFile(filePath, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor, err := NewFilesystemToolExecutor(FilesystemToolConfig{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"path": filePath})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "fs.read", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tool != "fs.read" || len(result.Output) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
