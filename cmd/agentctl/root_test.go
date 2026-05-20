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

func TestSchemaCommandPrintsScenarioJSONSchema(t *testing.T) {
	cmd := newRootCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"schema"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("schema command failed: %v stderr=%s", err, stderr.String())
	}

	var schema map[string]any
	if err := json.Unmarshal(out.Bytes(), &schema); err != nil {
		t.Fatalf("schema output should be valid JSON, got %q: %v", out.String(), err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("unexpected schema draft: %+v", schema["$schema"])
	}
	if schema["title"] != "AgentFlow Scenario" {
		t.Fatalf("unexpected schema title: %+v", schema["title"])
	}
	if !bytes.Contains(out.Bytes(), []byte(`"fixed_workflow"`)) {
		t.Fatalf("schema output should include orchestration enum: %s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"tool_call"`)) {
		t.Fatalf("schema output should include llm capability enum: %s", out.String())
	}
}

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

func TestRunAndResumeContinueCompletesRun(t *testing.T) {
	stateDir := t.TempDir()
	secret := "test-secret"

	runCmd := newRootCommand()
	var runOut, runErr bytes.Buffer
	runCmd.SetOut(&runOut)
	runCmd.SetErr(&runErr)
	runCmd.SetArgs([]string{
		"run",
		"-f", filepath.Join("..", "..", "examples", "human_in_loop.yaml"),
		"--run-id", "cli-hitl-continue",
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

	resumeCmd := newRootCommand()
	var resumeOut, resumeErr bytes.Buffer
	resumeCmd.SetOut(&resumeOut)
	resumeCmd.SetErr(&resumeErr)
	resumeCmd.SetArgs([]string{
		"resume",
		"-f", filepath.Join("..", "..", "examples", "human_in_loop.yaml"),
		"--continue",
		"--token", result.Token,
		"--decision", "approve",
		"--token-secret", secret,
		"--state-dir", stateDir,
		"--json",
	})
	if err := resumeCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resume --continue failed: %v stderr=%s", err, resumeErr.String())
	}
	var continued runstateResult
	if err := json.Unmarshal(resumeOut.Bytes(), &continued); err != nil {
		t.Fatalf("resume stdout should be JSON, got %q: %v", resumeOut.String(), err)
	}
	if continued.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run, got %+v", continued)
	}
}

func TestRunFixedWorkflowExample(t *testing.T) {
	runCmd := newRootCommand()
	var runOut, runErr bytes.Buffer
	runCmd.SetOut(&runOut)
	runCmd.SetErr(&runErr)
	runCmd.SetArgs([]string{
		"run",
		"-f", filepath.Join("..", "..", "examples", "fixed_workflow.yaml"),
		"--run-id", "cli-fixed-workflow",
		"--prompt", "review",
		"--json",
	})
	if err := runCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("run command failed: %v stderr=%s", err, runErr.String())
	}
	var result runstateResult
	if err := json.Unmarshal(runOut.Bytes(), &result); err != nil {
		t.Fatalf("run command stdout should be one JSON result, got %q: %v", runOut.String(), err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed fixed workflow, got %+v body=%s", result, runOut.String())
	}
}

type runstateResult struct {
	Status runstate.RunStatus `json:"status"`
	Token  string             `json:"token"`
}
