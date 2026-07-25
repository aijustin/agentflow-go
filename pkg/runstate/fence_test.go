package runstate_test

import (
	"context"
	"errors"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestSaveWithFenceWithoutTokenUsesPlainSave(t *testing.T) {
	repo := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-plain", Status: runstate.RunStatusRunning}

	fellBack, err := runstate.SaveWithFence(context.Background(), repo, &snapshot, 0)
	if err != nil || fellBack {
		t.Fatalf("tokenless save must be a plain save: fellBack=%v err=%v", fellBack, err)
	}
}

func TestSaveWithFenceRequiresFencedRepositoryWhenTokenPresent(t *testing.T) {
	repo := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-fence", Status: runstate.RunStatusRunning}

	type unfencedRepo struct{ runstate.Repository }

	fellBack, err := runstate.SaveWithFence(
		runstate.ContextWithFenceToken(context.Background(), 7),
		unfencedRepo{repo},
		&snapshot,
		0,
	)
	if !errors.Is(err, runstate.ErrFenceRequired) || fellBack {
		t.Fatalf("expected ErrFenceRequired without fallback: fellBack=%v err=%v", fellBack, err)
	}
}

func TestSaveWithFenceUsesFencedSaveWhenSupported(t *testing.T) {
	repo := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-fenced", Status: runstate.RunStatusRunning}

	fellBack, err := runstate.SaveWithFence(
		runstate.ContextWithFenceToken(context.Background(), 7),
		repo,
		&snapshot,
		0,
	)
	if err != nil || fellBack {
		t.Fatalf("fenced repo must save fenced: fellBack=%v err=%v", fellBack, err)
	}
}
