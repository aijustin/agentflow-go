package agentflow_test

import (
	"context"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func TestWiringOptionsRunsFixedWorkflowExample(t *testing.T) {
	scenario := builder.MinimalFixedWorkflowReview("reviewer")
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: "."})
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
	scenario := builder.CodeReviewPipeline()
	scenario.Orchestration.HumanInLoop.Enabled = false
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: "."})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		t.Fatal(err)
	}
}
