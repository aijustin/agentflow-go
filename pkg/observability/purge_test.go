package observability

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// stubEventStore implements only the base EventStore interface: none of the
// purge/sequenced extensions.
type stubEventStore struct{}

func (stubEventStore) Append(context.Context, core.Event) (EventRecord, error) {
	return EventRecord{}, nil
}
func (stubEventStore) ListRuns(context.Context, RunQuery) ([]RunSummary, error) { return nil, nil }
func (stubEventStore) ListEvents(context.Context, string, EventQuery) ([]EventRecord, error) {
	return nil, nil
}
func (stubEventStore) ListScopedEvents(context.Context, ScopedEventQuery) ([]EventRecord, error) {
	return nil, nil
}

// stubPurgerStore implements every extension and records calls.
type stubPurgerStore struct {
	stubEventStore
	runPurges  int
	agePurges  int
	sequenced  int
	lastRunID  string
	lastCutoff time.Time
	lastSeq    int64
}

func (s *stubPurgerStore) DeleteEventsForRun(_ context.Context, runID string) (int64, error) {
	s.runPurges++
	s.lastRunID = runID
	return 3, nil
}

func (s *stubPurgerStore) PurgeEventsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.agePurges++
	s.lastCutoff = cutoff
	return 7, nil
}

func (s *stubPurgerStore) AppendSequenced(_ context.Context, sequence int64, event core.Event) (EventRecord, error) {
	s.sequenced++
	s.lastSeq = sequence
	return EventRecord{Sequence: sequence, Event: event}, nil
}

func TestPurgeHelpersNoopWithoutExtension(t *testing.T) {
	ctx := context.Background()
	removed, err := DeleteEventsForRun(ctx, stubEventStore{}, "run-1")
	if err != nil || removed != 0 {
		t.Fatalf("expected no-op for unsupported store, got %d err=%v", removed, err)
	}
	removed, err = PurgeEventsBefore(ctx, stubEventStore{}, time.Now())
	if err != nil || removed != 0 {
		t.Fatalf("expected no-op for unsupported store, got %d err=%v", removed, err)
	}
	removed, err = DeleteEventsForRun(ctx, nil, "run-1")
	if err != nil || removed != 0 {
		t.Fatalf("expected no-op for nil store, got %d err=%v", removed, err)
	}
}

func TestPurgeHelpersDispatchToExtension(t *testing.T) {
	ctx := context.Background()
	store := &stubPurgerStore{}
	removed, err := DeleteEventsForRun(ctx, store, "run-9")
	if err != nil || removed != 3 || store.runPurges != 1 || store.lastRunID != "run-9" {
		t.Fatalf("expected dispatch to DeleteEventsForRun, got removed=%d err=%v store=%+v", removed, err, store)
	}
	cutoff := time.Now().UTC().Add(-time.Hour)
	removed, err = PurgeEventsBefore(ctx, store, cutoff)
	if err != nil || removed != 7 || store.agePurges != 1 || !store.lastCutoff.Equal(cutoff) {
		t.Fatalf("expected dispatch to PurgeEventsBefore, got removed=%d err=%v store=%+v", removed, err, store)
	}
	record, err := store.AppendSequenced(ctx, 11, core.Event{RunID: "run-9"})
	if err != nil || record.Sequence != 11 || store.lastSeq != 11 {
		t.Fatalf("expected sequenced append, got %+v err=%v", record, err)
	}
}
