//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestCrossProcessRunStateLoad(t *testing.T) {
	dir := t.TempDir()
	runDir := dir + "/runs"

	repo1, err := runstatefile.NewRepository(runDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snap := &runstate.RunSnapshot{
		RunID:        "run-cross",
		ScenarioName: "cross-process",
		Status:       runstate.RunStatusPaused,
		CurrentNodeID: "review",
		StepOutputs: map[string]runstate.StepOutputRef{
			"inspect": {Inline: []byte(`{"ok":true}`)},
		},
	}
	if err := repo1.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}

	repo2, err := runstatefile.NewRepository(runDir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo2.Load(ctx, "run-cross")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusPaused || loaded.CurrentNodeID != "review" {
		t.Fatalf("unexpected snapshot: %+v", loaded)
	}
	if !bytes.Equal(loaded.StepOutputs["inspect"].Inline, []byte(`{"ok":true}`)) &&
		!bytes.Equal(bytes.TrimSpace(loaded.StepOutputs["inspect"].Inline), []byte(`{"ok":true}`)) {
		// file repo may normalize JSON whitespace
		var got, want map[string]any
		if err := json.Unmarshal(loaded.StepOutputs["inspect"].Inline, &got); err != nil {
			t.Fatalf("step output not preserved: %+v", loaded.StepOutputs["inspect"])
		}
		if err := json.Unmarshal([]byte(`{"ok":true}`), &want); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got["ok"]) != fmt.Sprint(want["ok"]) {
			t.Fatalf("step output not preserved: %+v", loaded.StepOutputs["inspect"])
		}
	}
}

func TestCrossProcessRunStateCAS(t *testing.T) {
	dir := t.TempDir()
	runDir := dir + "/runs"

	repo1, err := runstatefile.NewRepository(runDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snap := &runstate.RunSnapshot{
		RunID:        "run-cas",
		ScenarioName: "cross-process",
		Status:       runstate.RunStatusRunning,
		Version:      1,
	}
	if err := repo1.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}

	repo2, err := runstatefile.NewRepository(runDir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo2.Load(ctx, "run-cas")
	if err != nil {
		t.Fatal(err)
	}
	loaded.Status = runstate.RunStatusCompleted
	if err := repo2.Save(ctx, &loaded, 1); err != nil {
		t.Fatal(err)
	}

	final, err := repo1.Load(ctx, "run-cas")
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != runstate.RunStatusCompleted || final.Version != 2 {
		t.Fatalf("unexpected final snapshot: %+v", final)
	}
}
