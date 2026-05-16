package inmem

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestRepositoryIsolatesNamespaces(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	ns1 := memory.Namespace{RunID: "run-1", SessionID: "session", Agent: "agent", Scope: memory.ScopeSession}
	ns2 := memory.Namespace{RunID: "run-2", SessionID: "session", Agent: "agent", Scope: memory.ScopeSession}

	if err := repo.Set(ctx, ns1, "key", json.RawMessage(`"value-1"`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Set(ctx, ns2, "key", json.RawMessage(`"value-2"`)); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, ns1, "key")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"value-1"` {
		t.Fatalf("expected namespace isolation, got %s", got)
	}
}

func TestRepositoryConcurrentAccess(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	ns := memory.Namespace{RunID: "run", SessionID: "session", Scope: memory.ScopeSession}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.Append(ctx, ns, "events", json.RawMessage(`{"ok":true}`)); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := repo.Get(ctx, ns, "events")
	if err != nil {
		t.Fatal(err)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(got, &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 100 {
		t.Fatalf("expected 100 values, got %d", len(values))
	}
}

func TestRepositoryNotFound(t *testing.T) {
	repo := NewRepository()
	_, err := repo.Get(context.Background(), memory.Namespace{Scope: memory.ScopeSession}, "missing")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
