package file

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestRepositoryPersistsMemory(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ns := memory.Namespace{SessionID: "session", Agent: "agent", Scope: memory.ScopeSession}
	if err := repo.Append(ctx, ns, "messages", json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Append(ctx, ns, "messages", json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewRepository(repo.dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := reopened.Get(ctx, ns, "messages")
	if err != nil {
		t.Fatal(err)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if err := reopened.Delete(ctx, ns, "messages"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(ctx, ns, "messages"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}
