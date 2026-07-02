package inmem

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestCheckpointHistoryAppendListLoad(t *testing.T) {
	h := NewCheckpointHistory()
	ctx := context.Background()
	for version := int64(1); version <= 3; version++ {
		if err := h.Append(ctx, runstate.RunSnapshot{RunID: "run-1", Version: version, Status: runstate.RunStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Append(ctx, runstate.RunSnapshot{RunID: "run-1", Version: 2, Status: runstate.RunStatusRunning}); err != nil {
		t.Fatal(err)
	}
	list, err := h.List(ctx, "run-1", 0)
	if err != nil || len(list) != 3 {
		t.Fatalf("expected 3 checkpoints, got %+v err=%v", list, err)
	}
	limited, err := h.List(ctx, "run-1", 2)
	if err != nil || len(limited) != 2 || limited[0].Version != 2 {
		t.Fatalf("expected last two versions, got %+v err=%v", limited, err)
	}
	snap, err := h.Load(ctx, "run-1", 2)
	if err != nil || snap.Version != 2 {
		t.Fatalf("unexpected snapshot: %+v err=%v", snap, err)
	}
	if _, err := h.Load(ctx, "run-1", 99); !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
