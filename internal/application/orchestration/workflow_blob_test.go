package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestWorkflowRunnerStepOutputRawLoadsBlobRef(t *testing.T) {
	runs := newWorkflowRun(t)
	blobs := blobinmem.NewStore()
	runner := NewWorkflowRunner(nil, runs, nil, WithBlobStore(blobs))
	ctx := context.Background()
	raw := json.RawMessage(`{"answer":"stored externally"}`)
	ref, err := blobs.Put(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := runs.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-blob", Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"prep": {Blob: &ref},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	got, ok, err := runner.stepOutputRaw(ctx, "run-blob", "prep")
	if err != nil || !ok || string(got) != string(raw) {
		t.Fatalf("stepOutputRaw=%q ok=%v err=%v", got, ok, err)
	}
}

func TestWorkflowRunnerPauseWithRetryRecoversFromStaleSnapshot(t *testing.T) {
	runs := newWorkflowRun(t)
	gate := &staleOnceGate{workflowGate: &workflowGate{repo: runs}}
	runner := NewWorkflowRunner(nil, runs, nil, WithHumanGate(gate))
	ctx := context.Background()
	if err := runs.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-stale-pause", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	token, err := runner.pauseWithRetry(ctx, "run-stale-pause", nil, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: "run-stale-pause", Version: version, NodeID: "review"}
	})
	if err != nil || token == "" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if gate.calls != 2 {
		t.Fatalf("expected stale retry, pause calls=%d", gate.calls)
	}
}

func TestWorkflowRunnerResolveWorkflowPath(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	ctx := context.Background()
	if err := runs.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-path", Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"prep": {Inline: json.RawMessage(`{"items":[{"id":1},{"id":2}]}`)},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.resolveWorkflowPath(ctx, "run-path", "bad.path"); err == nil {
		t.Fatal("expected invalid path prefix error")
	}
	value, ok, err := runner.resolveWorkflowPath(ctx, "run-path", "steps.prep.items.1.id")
	if err != nil || !ok {
		t.Fatalf("resolve nested path: value=%v ok=%v err=%v", value, ok, err)
	}
	if value != float64(2) {
		t.Fatalf("unexpected nested value: %#v", value)
	}
	missing, ok, err := runner.resolveWorkflowPath(ctx, "run-path", "steps.prep.missing")
	if err != nil || ok {
		t.Fatalf("expected missing key to be not found, value=%v ok=%v err=%v", missing, ok, err)
	}
}

func TestWorkflowRunnerStepOutputRawRequiresBlobStore(t *testing.T) {
	runs := newWorkflowRun(t)
	blobs := blobinmem.NewStore()
	ctx := context.Background()
	ref, err := blobs.Put(ctx, json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := runs.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-no-blob", Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{"prep": {Blob: &ref}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(nil, runs, nil)
	if _, _, err := runner.stepOutputRaw(ctx, "run-no-blob", "prep"); err == nil {
		t.Fatal("expected blob store required error")
	}
}

type staleOnceGate struct {
	*workflowGate
	calls int
}

func (g *staleOnceGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	g.calls++
	if g.calls == 1 {
		return "", runstate.ErrStaleSnapshot
	}
	return g.workflowGate.Pause(ctx, state)
}
