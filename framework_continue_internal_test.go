package agentflow

import (
	"context"
	"encoding/json"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRunIDFromTokenUsesPausedRunID(t *testing.T) {
	runs := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "paused-run", Status: runstate.RunStatusPaused}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	fw := &Framework{runs: runs}
	runID, err := fw.runIDFromToken(context.Background(), "paused-run")
	if err != nil || runID != "paused-run" {
		t.Fatalf("runID=%q err=%v", runID, err)
	}
}

func TestApplyWorkflowAmendmentPromotesHumanFeedback(t *testing.T) {
	runs := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{
		RunID:  "run-1",
		Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"human_amendment": json.RawMessage(`"please revise"`),
		},
	}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	fw := &Framework{runs: runs}
	if err := fw.applyWorkflowAmendment(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := variableJSONString(loaded.Variables, "workflow_amendment"); got != "please revise" {
		t.Fatalf("workflow_amendment=%q", got)
	}
	if got := variableJSONString(loaded.Variables, resumePromptVar); got != "please revise" {
		t.Fatalf("resume_prompt=%q", got)
	}
	if _, ok := loaded.Variables["human_amendment"]; ok {
		t.Fatal("expected human_amendment cleared")
	}
}

func TestVariableJSONString(t *testing.T) {
	vars := map[string]json.RawMessage{
		"plain":  json.RawMessage(`hello`),
		"quoted": json.RawMessage(`"world"`),
	}
	if got := variableJSONString(vars, "plain"); got != "hello" {
		t.Fatalf("plain=%q", got)
	}
	if got := variableJSONString(vars, "quoted"); got != "world" {
		t.Fatalf("quoted=%q", got)
	}
	if got := variableJSONString(vars, "missing"); got != "" {
		t.Fatalf("missing=%q", got)
	}
}
