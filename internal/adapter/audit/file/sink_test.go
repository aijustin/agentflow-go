package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
)

func TestSinkWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "events.jsonl")
	sink, err := NewSink(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	sink.now = func() time.Time { return now }
	if err := sink.Record(context.Background(), audit.Event{ID: "evt-1", Type: audit.EventRunSubmitted}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event audit.Event
	if err := json.Unmarshal(data[:len(data)-1], &event); err != nil {
		t.Fatal(err)
	}
	if event.ID != "evt-1" || event.Type != audit.EventRunSubmitted || !event.Timestamp.Equal(now) {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestNewSinkRequiresPath(t *testing.T) {
	if _, err := NewSink(""); err == nil {
		t.Fatal("expected missing path error")
	}
}
