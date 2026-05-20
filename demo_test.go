package agentflow_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestDemoOptionsRunsFixedWorkflowExample(t *testing.T) {
	scenarioPath := filepath.Join("examples", "fixed_workflow.yaml")
	workDir, err := agentflow.DemoWorkDir(scenarioPath)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := agentflow.LoadScenarioFile(scenarioPath)
	if err != nil {
		t.Fatal(err)
	}
	demoOpts, err := agentflow.DemoOptions(scenario, agentflow.DemoConfig{WorkDir: workDir})
	if err != nil {
		t.Fatal(err)
	}
	fw, err := agentflow.New(scenario, demoOpts...)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "demo-workflow", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed workflow, got %+v", result)
	}
}

func TestDemoOptionsRegistersGitTool(t *testing.T) {
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("requires git repository checkout")
	}
	scenarioPath := filepath.Join("examples", "code_review_pipeline.yaml")
	workDir, err := agentflow.DemoWorkDir(scenarioPath)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := agentflow.LoadScenarioFile(scenarioPath)
	if err != nil {
		t.Fatal(err)
	}
	demoOpts, err := agentflow.DemoOptions(scenario, agentflow.DemoConfig{WorkDir: workDir})
	if err != nil {
		t.Fatal(err)
	}
	opts := append(demoOpts, agentflow.WithHITLTokenSecret([]byte("test"), io.Discard))
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "demo-review", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused run at human gate, got %+v", result)
	}
	if result.Token == "" {
		t.Fatalf("expected approval token, got %+v", result)
	}
}
