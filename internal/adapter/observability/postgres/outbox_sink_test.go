package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

type recordingPublisher struct {
	mu      sync.Mutex
	records []obspkg.EventRecord
}

func (p *recordingPublisher) PublishEvent(_ context.Context, record obspkg.EventRecord) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, record)
	return nil
}

func (p *recordingPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.records)
}

func TestOutboxSinkAppendsAndPublishesOnSuccess(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	sink, err := NewOutboxSink(OutboxSinkConfig{Store: store, Publishers: []obspkg.EventPublisher{publisher}})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventRunStarted, RunID: "run-1", Timestamp: time.Now().UTC()}
	if err := sink.Emit(ctx, event); err != nil {
		t.Fatal(err)
	}
	if publisher.count() != 1 {
		t.Fatalf("expected live publication, got %d", publisher.count())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.outbox) != 0 {
		t.Fatalf("success path must not park events, got %+v", state.outbox)
	}
	if len(state.rows) != 1 || state.rows[0].sequence != 1 {
		t.Fatalf("expected direct append at sequence 1, got %+v", state.rows)
	}
}

// When the durable append fails, the event is parked in the outbox with the
// run's next sequence and Emit reports success so the runtime's lifecycle
// retry does not re-append a duplicate.
func TestOutboxSinkParksEventWhenAppendFails(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	sink, err := NewOutboxSink(OutboxSinkConfig{Store: store, Publishers: []obspkg.EventPublisher{publisher}})
	if err != nil {
		t.Fatal(err)
	}
	// One event lands directly first (sequence 1), then the store goes down.
	if err := sink.Emit(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-1", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.failAppend = true
	state.mu.Unlock()
	parked := core.Event{Type: core.EventRunCompleted, RunID: "run-1", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{"text":"done"}`)}
	if err := sink.Emit(ctx, parked); err != nil {
		t.Fatalf("parked emit must report success, got %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.outbox) != 1 {
		t.Fatalf("expected one parked outbox row, got %+v", state.outbox)
	}
	row := state.outbox[0]
	if row.runID != "run-1" || row.sequence != 2 {
		t.Fatalf("parked row must continue the run sequence at 2, got %+v", row)
	}
	var decoded core.Event
	if err := json.Unmarshal(row.payload, &decoded); err != nil {
		t.Fatalf("parked payload must be the full event envelope: %v", err)
	}
	if decoded.Type != core.EventRunCompleted || decoded.RunID != "run-1" {
		t.Fatalf("parked event mismatch: %+v", decoded)
	}
	// Live publication is skipped for parked events (same as a failed append
	// before the outbox existed).
	if publisher.count() != 1 {
		t.Fatalf("expected no live publication for parked event, got %d", publisher.count())
	}
}

// The parked sequence must account for earlier parked rows, not only the
// event table.
func TestOutboxSinkParkContinuesOverParkedRows(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewOutboxSink(OutboxSinkConfig{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.failAppend = true
	state.mu.Unlock()
	for i := 0; i < 2; i++ {
		if err := sink.Emit(ctx, core.Event{Type: core.EventRunFailed, RunID: "run-2", Timestamp: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.outbox) != 2 || state.outbox[0].sequence != 1 || state.outbox[1].sequence != 2 {
		t.Fatalf("expected parked sequences 1,2 got %+v", state.outbox)
	}
}

func TestOutboxSinkValidation(t *testing.T) {
	if _, err := NewOutboxSink(OutboxSinkConfig{}); err == nil {
		t.Fatal("expected nil store error")
	}
	db, _ := openTestDB(t)
	store, err := NewStore(context.Background(), Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewOutboxSink(OutboxSinkConfig{Store: store, OutboxTableName: "bad;name"}); err == nil {
		t.Fatal("expected invalid outbox table name error")
	}
}

func TestAppendSequencedInsertsWithGivenSequence(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventRunCompleted, RunID: "run-9", Timestamp: time.Now().UTC()}
	record, err := store.AppendSequenced(ctx, 7, event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Sequence != 7 || record.Event.RunID != "run-9" {
		t.Fatalf("unexpected record: %+v", record)
	}
	state.mu.Lock()
	if len(state.rows) != 1 || state.rows[0].sequence != 7 {
		t.Fatalf("expected stored row at sequence 7, got %+v", state.rows)
	}
	state.mu.Unlock()
	// A regular Append afterwards continues past the sequenced insert.
	next, err := store.Append(ctx, core.Event{Type: core.EventRunPaused, RunID: "run-9", Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 8 {
		t.Fatalf("expected Append to continue at 8, got %d", next.Sequence)
	}
}

// Redelivery with the same (run_id, sequence) is a no-op success: the relay
// relies on this to treat conflicts as delivered.
func TestAppendSequencedConflictIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventRunCompleted, RunID: "run-9", Timestamp: time.Now().UTC()}
	first, err := store.AppendSequenced(ctx, 3, event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendSequenced(ctx, 3, event)
	if err != nil {
		t.Fatalf("conflict must be reported as success, got %v", err)
	}
	if second.ID != first.ID || second.Sequence != 3 {
		t.Fatalf("conflict must return the existing row, got %+v want id %d", second, first.ID)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.rows) != 1 {
		t.Fatalf("duplicate delivery must not insert, got %d rows", len(state.rows))
	}
}

func TestDeleteEventsForRunAndPurgeBefore(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	fresh := time.Now().UTC()
	for _, event := range []core.Event{
		{Type: core.EventRunStarted, RunID: "run-a", Timestamp: old},
		{Type: core.EventRunCompleted, RunID: "run-a", Timestamp: fresh},
		{Type: core.EventRunStarted, RunID: "run-b", Timestamp: fresh},
	} {
		if _, err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.DeleteEventsForRun(ctx, "run-b")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 event deleted for run-b, got %d", removed)
	}
	events, err := store.ListEvents(ctx, "run-b", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("run-b events must be gone, got %+v", events)
	}
	removed, err = store.PurgeEventsBefore(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 old event purged, got %d", removed)
	}
	events, err = store.ListEvents(ctx, "run-a", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event.Type != core.EventRunCompleted {
		t.Fatalf("only the fresh run-a event must remain, got %+v", events)
	}
}

// A parked row whose park ALSO fails surfaces both errors so the runtime
// logs the loss like before.
func TestOutboxSinkSurfacesErrorWhenParkFails(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewOutboxSink(OutboxSinkConfig{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.failAppend = true
	state.mu.Unlock()
	// An event without a run id fails both the append and the park.
	err = sink.Emit(ctx, core.Event{Type: core.EventRunCompleted, Timestamp: time.Now().UTC()})
	if err == nil {
		t.Fatal("expected joined error when both append and park fail")
	}
	if !strings.Contains(err.Error(), "run id is required") {
		t.Fatalf("expected cause in error, got %v", err)
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected joined error, got %T", err)
	}
}
