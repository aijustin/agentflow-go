package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRunAndResumeShareFileBackedState(t *testing.T) {
	stateDir := t.TempDir()
	secret := "test-secret"

	runCmd := newRootCommand()
	var runOut, runErr bytes.Buffer
	runCmd.SetOut(&runOut)
	runCmd.SetErr(&runErr)
	runCmd.SetArgs([]string{
		"run",
		"-f", filepath.Join("..", "..", "examples", "human_in_loop.yaml"),
		"--run-id", "cli-hitl",
		"--prompt", "approval",
		"--token-secret", secret,
		"--state-dir", stateDir,
		"--json",
	})
	if err := runCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("run command failed: %v stderr=%s", err, runErr.String())
	}
	var result runstateResult
	if err := json.Unmarshal(runOut.Bytes(), &result); err != nil {
		t.Fatalf("run command stdout should be one JSON result, got %q: %v", runOut.String(), err)
	}
	if result.Status != runstate.RunStatusPaused || result.Token == "" {
		t.Fatalf("unexpected run result: %+v", result)
	}

	resumeCmd := newRootCommand()
	var resumeOut, resumeErr bytes.Buffer
	resumeCmd.SetOut(&resumeOut)
	resumeCmd.SetErr(&resumeErr)
	resumeCmd.SetArgs([]string{
		"resume",
		"--token", result.Token,
		"--decision", "approve",
		"--token-secret", secret,
		"--state-dir", stateDir,
	})
	if err := resumeCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resume command failed: %v stderr=%s", err, resumeErr.String())
	}

	repo, err := runstatefile.NewRepository(filepath.Join(stateDir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(context.Background(), "cli-hitl")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusRunning || snapshot.PendingGate != nil {
		t.Fatalf("expected resumed snapshot, got %+v", snapshot)
	}
}

type runstateResult struct {
	Status runstate.RunStatus `json:"status"`
	Token  string             `json:"token"`
}
