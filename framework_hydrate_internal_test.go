package agentflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestWorkflowRunOutputLoadsBlobFinal(t *testing.T) {
	blobs := blobinmem.NewStore()
	payload := json.RawMessage(`{"answer":"from-blob"}`)
	ref, err := blobs.Put(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	fw := &Framework{blobs: blobs}
	snapshot := runstate.RunSnapshot{
		RunID:  "run-blob-final",
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"prep":  {Inline: json.RawMessage(`{"ready":true}`)},
			"final": {Blob: &ref},
		},
	}
	out, err := fw.workflowRunOutput(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(payload) {
		t.Fatalf("got %q, want blob final payload", out)
	}
	if strings.Contains(out, `"steps"`) {
		t.Fatalf("must not return hydrated steps envelope: %s", out)
	}
}

func TestCompletedHybridResultReturnsFinalOutput(t *testing.T) {
	fw := &Framework{
		scenario: core.Scenario{Name: "hybrid"},
	}
	snapshot := runstate.RunSnapshot{
		RunID:  "run-done",
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"final": {Inline: json.RawMessage(`{"answer":"ok"}`)},
		},
	}
	result, ok, err := completedHybridResult(context.Background(), fw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || result.Status != runstate.RunStatusCompleted || result.Output != `{"answer":"ok"}` {
		t.Fatalf("result=%+v ok=%v", result, ok)
	}
}

func TestContinueHybridRunReturnsCompletedResult(t *testing.T) {
	runs := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{
		RunID:  "run-done",
		Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"final": {Inline: json.RawMessage(`"done"`)},
		},
	}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	fw := &Framework{
		scenario: core.Scenario{
			Name:          "hybrid",
			Orchestration: core.Orchestration{Mode: core.OrchestrationHybrid},
		},
		runs: runs,
	}
	result, err := fw.continueHybridRun(context.Background(), "run-done", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != `"done"` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHybridRunRequestReadsResumeVariables(t *testing.T) {
	req := hybridRunRequest(runstate.RunSnapshot{
		RunID: "run-1",
		Variables: map[string]json.RawMessage{
			resumeAgentVar:     json.RawMessage(`"assistant"`),
			resumePromptVar:    json.RawMessage(`"continue"`),
			resumeTrustModeVar: json.RawMessage(`"full_trust"`),
			"input":            json.RawMessage(`{"topic":"billing"}`),
		},
	})
	if req.Agent != "assistant" || req.Prompt != "continue" || string(req.Context) != `{"topic":"billing"}` {
		t.Fatalf("req=%+v", req)
	}
	if req.TrustMode != TrustModeFullTrust {
		t.Fatalf("expected TrustModeFullTrust, got %q", req.TrustMode)
	}
}
