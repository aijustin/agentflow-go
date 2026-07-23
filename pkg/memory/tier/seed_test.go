package tier

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func seedNS() memory.Namespace {
	return memory.Namespace{Scope: memory.ScopeSession, SessionID: "sess-1", Agent: "assistant"}
}

// TestMessageRecordFields pins the field-population contract hosts rely on
// when hydrating chat history: the record mirrors the runtime's own write
// shape (role categories, role-derived importance, role/kind/searchable
// metadata, hot default tier).
func TestMessageRecordFields(t *testing.T) {
	ns := seedNS()
	ts := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	record, err := MessageRecord(ns, ChatMessage{Role: "user", Content: "hello world", Time: ts})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID == "" {
		t.Fatal("expected a generated record id")
	}
	if record.Tier != LevelHot {
		t.Fatalf("default tier must be hot, got %q", record.Tier)
	}
	if record.Scope != string(ns.Scope) {
		t.Fatalf("scope mismatch: %q", record.Scope)
	}
	if len(record.Categories) != 1 || record.Categories[0] != "user" {
		t.Fatalf("categories must be [role], got %v", record.Categories)
	}
	if record.Importance != memory.ImportanceForRole("user") {
		t.Fatalf("importance must derive from role, got %v", record.Importance)
	}
	if !record.CreatedAt.Equal(ts) || !record.LastAccessAt.Equal(ts) {
		t.Fatalf("timestamps must come from the message, got %v / %v", record.CreatedAt, record.LastAccessAt)
	}
	for key, want := range map[string]string{"role": "user", "kind": "message", "searchable": "hello world"} {
		if got := record.Metadata[key]; got != want {
			t.Fatalf("metadata[%q] = %q, want %q", key, got, want)
		}
	}
	var decoded ChatMessage
	if err := json.Unmarshal([]byte(record.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Role != "user" || decoded.Content != "hello world" || !decoded.Time.Equal(ts) {
		t.Fatalf("wire content mismatch: %+v", decoded)
	}
}

// TestMessageRecordProvenance: WithProvenance records the marker inside the
// message metadata (where the runtime puts it), not on the record envelope.
func TestMessageRecordProvenance(t *testing.T) {
	record, err := MessageRecord(seedNS(), ChatMessage{
		Role: "assistant", Content: "hi", Time: time.Now().UTC(),
	}, WithProvenance(memory.ProvenanceIntegrator))
	if err != nil {
		t.Fatal(err)
	}
	var decoded ChatMessage
	if err := json.Unmarshal([]byte(record.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Metadata[memory.ProvenanceKey]; got != memory.ProvenanceIntegrator {
		t.Fatalf("provenance must live in message metadata, got %q", got)
	}
}

// TestRecencyDominantRecallApproximatesFlatTail: with a large budget and
// recency-dominant weights, recall selection approximates the flat
// repository's "most recent N, replayed in order" behavior.
func TestRecencyDominantRecallApproximatesFlatTail(t *testing.T) {
	store := newTestStore()
	manager := NewManagerWithWeights(store, SingleLevelPolicy(), nil,
		memory.RecallWeights{Semantic: 0.01, Recency: 1.0, Importance: 0.01}, nil)
	ns := seedNS()
	base := time.Now().UTC().Add(-5 * time.Hour)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		record, err := MessageRecord(ns, ChatMessage{Role: "user", Content: "msg", Time: ts})
		if err != nil {
			t.Fatal(err)
		}
		record.ID = strings.Repeat(string(rune('a'+i)), 4)
		if err := store.Put(context.Background(), ns, record); err != nil {
			t.Fatal(err)
		}
	}
	recalled, err := manager.Recall(context.Background(), ns, "", RecallBudget{Total: 3, Hot: 3, Warm: 3, Cold: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 3 {
		t.Fatalf("expected the 3 most recent records, got %d", len(recalled))
	}
	// Selection picked the three newest; final order is chronological, so a
	// flat "tail replay" consumer sees the same sequence.
	wantFirst := base.Add(2 * time.Hour)
	if !recalled[0].CreatedAt.Equal(wantFirst) {
		t.Fatalf("oldest selected should be 3h old, got %v", recalled[0].CreatedAt.Sub(base))
	}
	for i := 1; i < len(recalled); i++ {
		if recalled[i].CreatedAt.Before(recalled[i-1].CreatedAt) {
			t.Fatal("recall output must be chronological")
		}
	}
}
