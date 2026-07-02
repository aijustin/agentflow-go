package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestCognitiveRepositoryRememberRecallAndUpdate(t *testing.T) {
	repo := NewCognitiveRepository()
	ctx := context.Background()
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", Agent: "assistant"}
	now := time.Now().UTC()

	if err := repo.Remember(ctx, ns, memory.CognitiveRecord{Content: "missing id"}); err == nil {
		t.Fatal("expected missing id error")
	}
	first := memory.CognitiveRecord{ID: "fact-1", Content: "prefers dark mode", Importance: 0.9, CreatedAt: now}
	if err := repo.Remember(ctx, ns, first); err != nil {
		t.Fatal(err)
	}
	updated := memory.CognitiveRecord{ID: "fact-1", Content: "prefers light mode", Importance: 0.5, CreatedAt: now}
	if err := repo.Remember(ctx, ns, updated); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Recall(ctx, ns, "light", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "prefers light mode" {
		t.Fatalf("unexpected recall: %+v", got)
	}
}

func TestAppendCognitiveFromMessages(t *testing.T) {
	repo := NewCognitiveRepository()
	ctx := context.Background()
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", Agent: "assistant"}
	if err := AppendCognitiveFromMessages(repo, ctx, ns, "user", "hello", 0.5); err != nil {
		t.Fatal(err)
	}
	if err := AppendCognitiveFromMessages(nil, ctx, ns, "user", "ignored", 0.5); err != nil {
		t.Fatal(err)
	}
	if err := AppendCognitiveFromMessages(repo, ctx, ns, "user", "", 0.5); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Recall(ctx, ns, "hello", 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("expected one record, got %+v err=%v", got, err)
	}
}
