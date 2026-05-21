package agentflow_test

import (
	"context"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func TestWiringOptionsRunsFixedWorkflowExample(t *testing.T) {
	scenario, err := agentflow.LoadScenarioFile("examples/fixed_workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	workDir, err := testutil.ScenarioWorkDir("examples/fixed_workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
	if err != nil {
		t.Fatal(err)
	}
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{
		RunID:  "demo-fixed",
		Prompt: "review this change",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != "demo-fixed" {
		t.Fatalf("unexpected run id: %s", result.RunID)
	}
}

func TestWiringOptionsRegistersGitTool(t *testing.T) {
	scenario, err := agentflow.LoadScenarioFile("examples/code_review_pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	scenario.Orchestration.HumanInLoop.Enabled = false
	workDir, err := testutil.ScenarioWorkDir("examples/code_review_pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		t.Fatal(err)
	}
}
