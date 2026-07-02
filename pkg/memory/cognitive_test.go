package memory

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNamespaceKeyPrefix(t *testing.T) {
	ns := Namespace{Scope: ScopeSession, SessionID: "sess", RunID: "run", Agent: "bot"}
	if got := ns.KeyPrefix(); got != "session:sess:run:bot" {
		t.Fatalf("unexpected prefix: %s", got)
	}
}

func TestRankMemories(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	records := []CognitiveRecord{
		{ID: "a", Content: "hello world", Importance: 0.9, CreatedAt: now.Add(-24 * time.Hour)},
		{ID: "b", Content: "other topic", Importance: 0.2, CreatedAt: now.Add(-48 * time.Hour)},
	}
	weights := DefaultRecallWeights().Normalize()
	ranked := RankMemories("hello", records, now, weights.Semantic, weights.Recency, weights.Importance)
	if len(ranked) != 2 || ranked[0].Record.ID != "a" {
		t.Fatalf("expected hello record first, got %+v", ranked)
	}
}

func TestEncodeDecodeRecord(t *testing.T) {
	record := CognitiveRecord{ID: "r1", Content: "note", Importance: 0.8, CreatedAt: time.Now().UTC()}
	raw, err := EncodeRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != record.ID || decoded.Content != record.Content {
		t.Fatalf("unexpected decoded record: %+v", decoded)
	}
}

func TestRecallWeightsAndImportance(t *testing.T) {
	w := RecallWeights{}.Normalize()
	if w.Semantic <= 0 || w.Recency <= 0 || w.Importance <= 0 {
		t.Fatalf("expected normalized weights: %+v", w)
	}
	if ImportanceForRole("user") <= ImportanceForRole("tool") {
		t.Fatal("expected user importance above tool")
	}
	if SearchableContent("  hello  ") != "hello" {
		t.Fatal("expected trimmed searchable content")
	}
}

func TestDecodeRecordInvalidJSON(t *testing.T) {
	if _, err := DecodeRecord(json.RawMessage(`{`)); err == nil {
		t.Fatal("expected decode error")
	}
}
