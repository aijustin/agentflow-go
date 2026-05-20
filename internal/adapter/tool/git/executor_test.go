package git

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestExecutorDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initTestRepo(t)
	executor, err := NewExecutor(Config{AllowedRoots: []string{repo}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "init")
	writeFile(t, filepath.Join(repo, "README.md"), "hello world\n")

	result, err := executor.Execute(t.Context(), core.ToolCall{
		Tool:  "git",
		Input: json.RawMessage(`{"action":"diff","repo":"` + repo + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error: %s output=%s", result.Error, string(result.Output))
	}
	if !strings.Contains(string(result.Output), "hello world") {
		t.Fatalf("expected diff output, got %s", string(result.Output))
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v %s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
