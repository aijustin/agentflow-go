package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func testEvent(runID string, typ core.EventType) core.Event {
	return core.Event{
		Type:      typ,
		RunID:     runID,
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage(`{"k":"v"}`),
	}
}

func outboxRows(state *testState) []testOutboxRow {
	state.mu.Lock()
	defer state.mu.Unlock()
	rows := make([]testOutboxRow, 0, len(state.outbox))
	for _, row := range state.outbox {
		rows = append(rows, row)
	}
	sortByID(rows)
	return rows
}

func sortByID(rows []testOutboxRow) {
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].id < rows[i].id {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
}

// Snapshot save and outbox inserts commit together: a successful
// SaveWithEvents leaves both the new snapshot version and the outbox rows.
func TestSaveWithEventsCommitsSnapshotAndOutbox(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-outbox", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	events := []core.Event{testEvent("run-outbox", core.EventRunStarted), testEvent("run-outbox", core.EventRunCompleted)}
	if err := repo.SaveWithEvents(ctx, &snapshot, 0, events, 7); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected version 1, got %d", snapshot.Version)
	}
	rows := outboxRows(state)
	if len(rows) != 2 {
		t.Fatalf("expected 2 outbox rows, got %d", len(rows))
	}
	for i, row := range rows {
		if row.runID != "run-outbox" {
			t.Fatalf("outbox row %d has run %q", i, row.runID)
		}
		if row.sequence != int64(i+1) {
			t.Fatalf("outbox row %d has sequence %d, want %d", i, row.sequence, i+1)
		}
		var decoded core.Event
		if err := json.Unmarshal(row.payload, &decoded); err != nil {
			t.Fatalf("outbox row %d payload must decode as full event: %v", i, err)
		}
		if decoded.Type != events[i].Type || decoded.RunID != "run-outbox" {
			t.Fatalf("outbox row %d decoded event mismatch: %+v", i, decoded)
		}
	}
	// A follow-up save with more events continues the same sequence space.
	if err := repo.SaveWithEvents(ctx, &snapshot, 1, []core.Event{testEvent("run-outbox", core.EventRunPaused)}, 7); err != nil {
		t.Fatal(err)
	}
	rows = outboxRows(state)
	if len(rows) != 3 || rows[2].sequence != 3 {
		t.Fatalf("expected third outbox row at sequence 3, got %+v", rows)
	}
}

// Sequence minting continues from the durable event table's per-run max, so
// outbox rows never collide with directly appended events.
func TestSaveWithEventsContinuesEventTableSequence(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.eventSeq["run-outbox"] = 5
	state.mu.Unlock()
	snapshot := runstate.RunSnapshot{RunID: "run-outbox", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveWithEvents(ctx, &snapshot, 0, []core.Event{testEvent("run-outbox", core.EventRunStarted)}, 0); err != nil {
		t.Fatal(err)
	}
	rows := outboxRows(state)
	if len(rows) != 1 || rows[0].sequence != 6 {
		t.Fatalf("expected sequence 6 after event-table max 5, got %+v", rows)
	}
}

// A fence conflict must roll the whole transaction back: neither the snapshot
// nor any outbox row survives.
func TestSaveWithEventsFenceConflictRollsBackOutbox(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &snapshot, 0, 9); err != nil {
		t.Fatal(err)
	}
	// A superseded holder (token 5 < recorded 9) attempts a save carrying
	// events; everything must be rejected.
	stale := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	err = repo.SaveWithEvents(ctx, &stale, 1, []core.Event{testEvent("run-fenced", core.EventRunPaused)}, 5)
	if !errors.Is(err, runstate.ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence, got %v", err)
	}
	if rows := outboxRows(state); len(rows) != 0 {
		t.Fatalf("fence conflict must leave no outbox rows, got %+v", rows)
	}
	loaded, err := repo.Load(ctx, "run-fenced")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.Status != runstate.RunStatusRunning {
		t.Fatalf("snapshot must be untouched after rollback, got %+v", loaded)
	}
}

// A stale version likewise rolls back both sides.
func TestSaveWithEventsStaleVersionRollsBackOutbox(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-stale", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	stale := runstate.RunSnapshot{RunID: "run-stale", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	err = repo.SaveWithEvents(ctx, &stale, 5, []core.Event{testEvent("run-stale", core.EventRunPaused)}, 0)
	if !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected ErrStaleSnapshot, got %v", err)
	}
	if rows := outboxRows(state); len(rows) != 0 {
		t.Fatalf("stale version must leave no outbox rows, got %+v", rows)
	}
}

// An event that fails marshaling aborts after the snapshot write was staged;
// the transaction rollback must discard the staged snapshot change too.
func TestSaveWithEventsEventErrorRollsBackSnapshot(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-bad-event", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	bad := testEvent("run-bad-event", core.EventRunCompleted)
	bad.Payload = json.RawMessage(`{invalid`)
	update := runstate.RunSnapshot{RunID: "run-bad-event", ScenarioName: "scenario", Status: runstate.RunStatusCompleted}
	if err := repo.SaveWithEvents(ctx, &update, 1, []core.Event{bad}, 0); err == nil {
		t.Fatal("expected marshal error")
	}
	loaded, err := repo.Load(ctx, "run-bad-event")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.Status != runstate.RunStatusRunning {
		t.Fatalf("staged snapshot change must roll back, got %+v", loaded)
	}
	if rows := outboxRows(state); len(rows) != 0 {
		t.Fatalf("no outbox rows expected, got %+v", rows)
	}
}

// Empty event batches degrade to the plain fenced/unfenced save paths.
func TestSaveWithEventsEmptyBatch(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-empty", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveWithEvents(ctx, &snapshot, 0, nil, 3); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected version 1, got %d", snapshot.Version)
	}
	if rows := outboxRows(state); len(rows) != 0 {
		t.Fatalf("no outbox rows expected, got %+v", rows)
	}
}

func TestOutboxFetchMarkAndPurge(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-relay", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	events := []core.Event{testEvent("run-relay", core.EventRunStarted), testEvent("run-relay", core.EventRunFailed)}
	if err := repo.SaveWithEvents(ctx, &snapshot, 0, events, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 unpublished rows, got %d", len(rows))
	}
	if rows[0].ID >= rows[1].ID {
		t.Fatalf("expected insertion order, got %d then %d", rows[0].ID, rows[1].ID)
	}
	if rows[0].Sequence != 1 || rows[0].Event.Type != core.EventRunStarted || rows[0].Event.RunID != "run-relay" {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	// Marking is idempotent and limited to the targeted row.
	if err := repo.MarkOutboxPublished(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOutboxPublished(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := repo.FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != rows[1].ID {
		t.Fatalf("expected only the second row unpublished, got %+v", remaining)
	}
	// Age-based purge removes only published rows.
	state.mu.Lock()
	published := state.outbox[rows[0].ID]
	published.publishedAt = time.Now().UTC().Add(-time.Hour)
	state.outbox[rows[0].ID] = published
	state.mu.Unlock()
	removed, err := repo.PurgeOutboxPublishedBefore(ctx, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 published row purged, got %d", removed)
	}
	remaining, err = repo.FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("unpublished rows must survive the purge, got %+v", remaining)
	}
	// Per-run cascade delete clears the rest.
	removed, err = repo.DeleteOutboxForRun(ctx, "run-relay")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 row deleted for run, got %d", removed)
	}
	remaining, err = repo.FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected empty outbox, got %+v", remaining)
	}
}

func TestPurgeOutboxPublishedBeforeScopesAuthenticatedTenant(t *testing.T) {
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		tenant string
		runID  string
	}{
		{tenant: "tenant-a", runID: "run-a"},
		{tenant: "tenant-b", runID: "run-b"},
	} {
		snapshot := runstate.RunSnapshot{
			RunID: spec.runID, ScenarioName: "scenario", TenantID: spec.tenant, Status: runstate.RunStatusRunning,
		}
		event := testEvent(spec.runID, core.EventRunStarted)
		event.TenantID = spec.tenant
		if err := repo.SaveWithEvents(context.Background(), &snapshot, 0, []core.Event{event}, 0); err != nil {
			t.Fatal(err)
		}
	}
	rows := outboxRows(state)
	for _, row := range rows {
		if err := repo.MarkOutboxPublished(context.Background(), row.id); err != nil {
			t.Fatal(err)
		}
	}
	state.mu.Lock()
	for id, row := range state.outbox {
		row.publishedAt = time.Now().UTC().Add(-time.Hour)
		state.outbox[id] = row
	}
	state.mu.Unlock()
	ctxA := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "admin-a", Type: identity.PrincipalUser,
		Scope: identity.Scope{TenantID: "tenant-a"}, Roles: []identity.Role{identity.RoleAdmin},
	})
	removed, err := repo.PurgeOutboxPublishedBefore(ctxA, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want tenant-a row only", removed)
	}
	remaining := outboxRows(state)
	if len(remaining) != 1 {
		t.Fatalf("remaining rows=%d, want 1", len(remaining))
	}
	var event core.Event
	if err := json.Unmarshal(remaining[0].payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.TenantID != "tenant-b" {
		t.Fatalf("remaining tenant=%q, want tenant-b", event.TenantID)
	}
}
