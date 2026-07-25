package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const testDriverName = "agentflow_postgres_repository_test"

var (
	registerTestDriver sync.Once
	testDBSeq          atomic.Int64
	testStatesMu       sync.Mutex
	testStates         = make(map[string]*testState)
)

func TestRepositorySaveStampsTimestamps(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot.CreatedAt.IsZero() || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps on save, got created=%v updated=%v", snapshot.CreatedAt, snapshot.UpdatedAt)
	}
	loaded, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CreatedAt.IsZero() || loaded.UpdatedAt.IsZero() {
		t.Fatalf("expected persisted timestamps, got %+v", loaded)
	}
}

func TestRepositorySavesLoadsAndDetectsStaleSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected version 1, got %d", snapshot.Version)
	}
	loaded, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != "run-1" || loaded.Version != 1 || loaded.Status != runstate.RunStatusRunning {
		t.Fatalf("unexpected loaded snapshot: %+v", loaded)
	}
	loaded.Status = runstate.RunStatusPaused
	if err := repo.Save(ctx, &loaded, 0); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected stale snapshot, got %v", err)
	}
	if err := repo.Save(ctx, &loaded, 1); err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 2 {
		t.Fatalf("expected version 2, got %d", loaded.Version)
	}
}

func TestRepositorySaveFenced(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &snapshot, 0, 5); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected version 1, got %d", snapshot.Version)
	}
	// Same token may keep writing (renew does not mint new tokens).
	snapshot.Status = runstate.RunStatusPaused
	if err := repo.SaveFenced(ctx, &snapshot, 1, 5); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("expected version 2, got %d", snapshot.Version)
	}
	// A newer holder takes over with a higher token.
	snapshot.Status = runstate.RunStatusRunning
	if err := repo.SaveFenced(ctx, &snapshot, 2, 9); err != nil {
		t.Fatal(err)
	}
	// The superseded holder's write is rejected with ErrStaleFence.
	zombie := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	if err := repo.SaveFenced(ctx, &zombie, 3, 5); !errors.Is(err, runstate.ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence for regressed token, got %v", err)
	}
	// A writer that is merely behind on version still sees ErrStaleSnapshot.
	behind := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &behind, 1, 10); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected ErrStaleSnapshot for stale version, got %v", err)
	}
	// SaveFenced against a missing run reports not found.
	missing := runstate.RunSnapshot{RunID: "run-missing", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &missing, 3, 1); !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Plain Save stays fence-agnostic and does not reset the fence.
	loaded, err := repo.Load(ctx, "run-fenced")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &loaded, loaded.Version); err != nil {
		t.Fatal(err)
	}
	zombie2 := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	if err := repo.SaveFenced(ctx, &zombie2, loaded.Version, 5); !errors.Is(err, runstate.ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence after plain Save, got %v", err)
	}
}

func TestRepositoryListStaleUsesGraceCutoff(t *testing.T) {
	ctx := context.Background()
	db, state := openTestDBWithState(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	fresh := runstate.RunSnapshot{RunID: "run-fresh", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &fresh, 0); err != nil {
		t.Fatal(err)
	}
	// Seed an old row directly: Save always stamps "now".
	oldSnapshot := runstate.RunSnapshot{RunID: "run-old", ScenarioName: "scenario", Status: runstate.RunStatusRunning, Version: 1}
	data, err := json.Marshal(oldSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.rows["run-old"] = testRow{version: 1, snapshot: data, updatedAt: time.Now().UTC().Add(-2 * time.Hour)}
	state.mu.Unlock()

	stale, err := repo.ListStale(ctx, runstate.ListFilter{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].RunID != "run-old" {
		t.Fatalf("expected only the old run to be stale, got %+v", stale)
	}
	// A zero grace makes everything stale; a huge grace nothing.
	all, err := repo.ListStale(ctx, runstate.ListFilter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected both runs with zero grace, got %d", len(all))
	}
	none, err := repo.ListStale(ctx, runstate.ListFilter{}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no stale runs with huge grace, got %d", len(none))
	}
}

func TestRepositoryRejectsInvalidStatusTransition(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusCompleted
	if err := repo.Save(ctx, &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusRunning
	if err := repo.Save(ctx, &snapshot, snapshot.Version); !errors.Is(err, runstate.ErrInvalidTransition) {
		t.Fatalf("expected invalid transition error, got %v", err)
	}
}

func TestRepositoryLoadMissingSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Load(ctx, "missing")
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestRepositoryDeletesSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Load(ctx, "run-1")
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestCheckpointHistoryAppendListLoad(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	history, err := NewCheckpointHistory(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{
		RunID:         "run-1",
		Version:       1,
		Status:        runstate.RunStatusRunning,
		CurrentNodeID: "review",
		StepOutputs: map[string]runstate.StepOutputRef{
			"prep": {Inline: json.RawMessage(`{"ok":true}`)},
		},
	}
	if err := history.Append(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Version = 2
	snapshot.Status = runstate.RunStatusCompleted
	if err := history.Append(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	list, err := history.List(ctx, "run-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(list))
	}
	if list[0].Version != 1 || list[1].Version != 2 {
		t.Fatalf("unexpected versions: %+v", list)
	}
	loaded, err := history.Load(ctx, "run-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusCompleted || loaded.CurrentNodeID != "review" {
		t.Fatalf("unexpected loaded snapshot: %+v", loaded)
	}
}

func TestNewRepositoryValidatesInputs(t *testing.T) {
	if _, err := NewRepository(nil); err == nil {
		t.Fatal("expected nil db error")
	}
	db := openTestDB(t)
	if _, err := NewRepository(db, WithTableName("agentflow.run_snapshots")); err != nil {
		t.Fatalf("expected schema-qualified table to be accepted: %v", err)
	}
	if _, err := NewRepository(db, WithTableName("bad;drop")); err == nil {
		t.Fatal("expected invalid table name error")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := openTestDBWithState(t)
	return db
}

func openTestDBWithState(t *testing.T) (*sql.DB, *testState) {
	t.Helper()
	registerTestDriver.Do(func() {
		sql.Register(testDriverName, testDriver{})
	})
	key := fmt.Sprintf("state-%d", testDBSeq.Add(1))
	state := &testState{
		rows:        make(map[string]testRow),
		checkpoints: make(map[string]testCheckpointRow),
		outbox:      make(map[int64]testOutboxRow),
		eventSeq:    make(map[string]int64),
	}
	testStatesMu.Lock()
	testStates[key] = state
	testStatesMu.Unlock()
	db, err := sql.Open(testDriverName, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		testStatesMu.Lock()
		delete(testStates, key)
		testStatesMu.Unlock()
	})
	return db, state
}

type testDriver struct{}

func (d testDriver) Open(name string) (driver.Conn, error) {
	testStatesMu.Lock()
	state := testStates[name]
	testStatesMu.Unlock()
	if state == nil {
		return nil, fmt.Errorf("unknown test database %q", name)
	}
	return &testConn{state: state}, nil
}

type testState struct {
	mu          sync.Mutex
	rows        map[string]testRow
	checkpoints map[string]testCheckpointRow
	outbox      map[int64]testOutboxRow
	outboxSeq   int64
	// eventSeq models the observability event table's per-run max sequence,
	// which SaveWithEvents/outbox parking must continue from.
	eventSeq map[string]int64
}

type testOutboxRow struct {
	id           int64
	runID        string
	sequence     int64
	eventType    string
	scenarioName string
	payload      []byte
	createdAt    time.Time
	publishedAt  time.Time
}

type testCheckpointRow struct {
	runID         string
	version       int64
	status        string
	currentNodeID string
	stepCount     int
	snapshot      []byte
	recordedAt    time.Time
}

type testRow struct {
	version    int64
	snapshot   []byte
	fenceToken int64
	updatedAt  time.Time
}

type testConn struct {
	state *testState
}

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by test driver")
}

func (c *testConn) Close() error { return nil }

// Begin snapshots the state so Rollback can restore it, giving tests real
// transaction-atomicity semantics for SaveWithEvents: anything staged inside
// the tx is discarded on rollback.
func (c *testConn) Begin() (driver.Tx, error) {
	c.state.mu.Lock()
	backup := &testStateBackup{
		rows:        make(map[string]testRow, len(c.state.rows)),
		checkpoints: make(map[string]testCheckpointRow, len(c.state.checkpoints)),
		outbox:      make(map[int64]testOutboxRow, len(c.state.outbox)),
		outboxSeq:   c.state.outboxSeq,
		eventSeq:    make(map[string]int64, len(c.state.eventSeq)),
	}
	for k, v := range c.state.rows {
		backup.rows[k] = v
	}
	for k, v := range c.state.checkpoints {
		backup.checkpoints[k] = v
	}
	for k, v := range c.state.outbox {
		backup.outbox[k] = v
	}
	for k, v := range c.state.eventSeq {
		backup.eventSeq[k] = v
	}
	c.state.mu.Unlock()
	return &testTx{state: c.state, backup: backup}, nil
}

type testStateBackup struct {
	rows        map[string]testRow
	checkpoints map[string]testCheckpointRow
	outbox      map[int64]testOutboxRow
	outboxSeq   int64
	eventSeq    map[string]int64
}

type testTx struct {
	state  *testState
	backup *testStateBackup
	done   bool
}

func (tx *testTx) Commit() error {
	tx.done = true
	return nil
}

func (tx *testTx) Rollback() error {
	if tx.done {
		return nil
	}
	tx.done = true
	tx.state.mu.Lock()
	tx.state.rows = tx.backup.rows
	tx.state.checkpoints = tx.backup.checkpoints
	tx.state.outbox = tx.backup.outbox
	tx.state.outboxSeq = tx.backup.outboxSeq
	tx.state.eventSeq = tx.backup.eventSeq
	tx.state.mu.Unlock()
	return nil
}

func (c *testConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "SELECT PG_ADVISORY"):
		// Advisory locks are no-ops in the single-process test driver.
		return driver.RowsAffected(0), nil
	case strings.HasPrefix(normalized, "INSERT INTO") && strings.Contains(normalized, "CHECKPOINT"):
		return c.insertCheckpoint(args)
	case strings.HasPrefix(normalized, "INSERT INTO") && strings.Contains(normalized, "AGENTFLOW_OUTBOX"):
		return c.insertOutbox(args)
	case strings.HasPrefix(normalized, "INSERT INTO") && strings.Contains(normalized, "FENCE_TOKEN"):
		return c.insertFenced(args)
	case strings.HasPrefix(normalized, "INSERT INTO"):
		return c.insert(args)
	case strings.HasPrefix(normalized, "UPDATE") && strings.Contains(normalized, "AGENTFLOW_OUTBOX"):
		return c.markOutboxPublished(args)
	case strings.HasPrefix(normalized, "UPDATE") && strings.Contains(normalized, "FENCE_TOKEN"):
		return c.updateFenced(args)
	case strings.HasPrefix(normalized, "UPDATE"):
		return c.update(args)
	case strings.HasPrefix(normalized, "DELETE FROM") && strings.Contains(normalized, "AGENTFLOW_OUTBOX") && strings.Contains(normalized, "PUBLISHED_AT"):
		return c.purgeOutboxPublished(args)
	case strings.HasPrefix(normalized, "DELETE FROM") && strings.Contains(normalized, "AGENTFLOW_OUTBOX"):
		return c.deleteOutboxForRun(args)
	case strings.HasPrefix(normalized, "DELETE FROM"):
		return c.delete(args)
	default:
		return nil, fmt.Errorf("unsupported exec query: %s", query)
	}
}

func (c *testConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "SELECT GREATEST"):
		return c.queryMaxEventSequence(args)
	case strings.HasPrefix(normalized, "SELECT ID, RUN_ID, SEQUENCE, PAYLOAD_JSON, CREATED_AT"):
		return c.queryUnpublishedOutbox(args)
	case strings.HasPrefix(normalized, "SELECT SNAPSHOT_JSON FROM") && strings.Contains(normalized, "MAKE_INTERVAL"):
		return c.queryStaleSnapshots(args)
	case strings.HasPrefix(normalized, "SELECT SNAPSHOT_JSON FROM"):
		if strings.Contains(normalized, "CHECKPOINT") {
			return c.queryCheckpointSnapshot(args)
		}
		return c.queryRunSnapshot(args)
	case strings.HasPrefix(normalized, "SELECT VERSION, FENCE_TOKEN FROM"):
		return c.queryFence(args)
	case strings.HasPrefix(normalized, "SELECT RUN_ID"):
		return c.queryCheckpointList(args)
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

func (c *testConn) queryRunSnapshot(args []driver.NamedValue) (driver.Rows, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	row, ok := c.state.rows[runID]
	c.state.mu.Unlock()
	if !ok {
		return &testRows{columns: []string{"snapshot_json"}}, nil
	}
	return &testRows{columns: []string{"snapshot_json"}, values: [][]driver.Value{{cloneBytes(row.snapshot)}}}, nil
}

func (c *testConn) queryCheckpointList(args []driver.NamedValue) (driver.Rows, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	rows := make([]testCheckpointRow, 0, len(c.state.checkpoints))
	for _, row := range c.state.checkpoints {
		if row.runID == runID {
			rows = append(rows, row)
		}
	}
	c.state.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].version < rows[j].version })
	values := make([][]driver.Value, 0, len(rows))
	for _, row := range rows {
		values = append(values, []driver.Value{row.runID, row.version, row.status, row.currentNodeID, row.stepCount, row.recordedAt})
	}
	return &testRows{
		columns: []string{"run_id", "version", "status", "current_node_id", "step_count", "recorded_at"},
		values:  values,
	}, nil
}

func (c *testConn) queryCheckpointSnapshot(args []driver.NamedValue) (driver.Rows, error) {
	runID := args[0].Value.(string)
	version := args[1].Value.(int64)
	c.state.mu.Lock()
	row, ok := c.state.checkpoints[checkpointKey(runID, version)]
	c.state.mu.Unlock()
	if !ok {
		return &testRows{columns: []string{"snapshot_json"}}, nil
	}
	return &testRows{columns: []string{"snapshot_json"}, values: [][]driver.Value{{cloneBytes(row.snapshot)}}}, nil
}

func (c *testConn) insertCheckpoint(args []driver.NamedValue) (driver.Result, error) {
	runID := args[0].Value.(string)
	version := args[1].Value.(int64)
	key := checkpointKey(runID, version)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if _, exists := c.state.checkpoints[key]; exists {
		return driver.RowsAffected(0), nil
	}
	c.state.checkpoints[key] = testCheckpointRow{
		runID:         runID,
		version:       version,
		status:        args[2].Value.(string),
		currentNodeID: args[3].Value.(string),
		stepCount:     driverInt(args[4].Value),
		snapshot:      valueBytes(args[5].Value),
		recordedAt:    args[6].Value.(time.Time),
	}
	return driver.RowsAffected(1), nil
}

func checkpointKey(runID string, version int64) string {
	return runID + "#" + fmt.Sprint(version)
}

func driverInt(value driver.Value) int {
	switch typed := value.(type) {
	case int64:
		return int(typed)
	case int:
		return typed
	case int32:
		return int(typed)
	default:
		return 0
	}
}

func (c *testConn) insert(args []driver.NamedValue) (driver.Result, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if _, exists := c.state.rows[runID]; exists {
		return driver.RowsAffected(0), nil
	}
	c.state.rows[runID] = testRow{version: args[1].Value.(int64), snapshot: valueBytes(args[8].Value), updatedAt: time.Now().UTC()}
	return driver.RowsAffected(1), nil
}

// insertFenced handles the SaveFenced insert, whose column list ends with
// fence_token: args[8] is the snapshot JSON and args[9] the fencing token.
func (c *testConn) insertFenced(args []driver.NamedValue) (driver.Result, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if _, exists := c.state.rows[runID]; exists {
		return driver.RowsAffected(0), nil
	}
	c.state.rows[runID] = testRow{
		version:    args[1].Value.(int64),
		snapshot:   valueBytes(args[8].Value),
		fenceToken: args[9].Value.(int64),
		updatedAt:  time.Now().UTC(),
	}
	return driver.RowsAffected(1), nil
}

func (c *testConn) update(args []driver.NamedValue) (driver.Result, error) {
	runID := args[8].Value.(string)
	expected := args[9].Value.(int64)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	row, exists := c.state.rows[runID]
	if !exists || row.version != expected {
		return driver.RowsAffected(0), nil
	}
	c.state.rows[runID] = testRow{version: args[0].Value.(int64), snapshot: valueBytes(args[7].Value), fenceToken: row.fenceToken, updatedAt: time.Now().UTC()}
	return driver.RowsAffected(1), nil
}

// updateFenced mirrors the SaveFenced update: args[8] run_id, args[9]
// expected version, args[10] fencing token, matched with fence_token <=
// token alongside the version CAS.
func (c *testConn) updateFenced(args []driver.NamedValue) (driver.Result, error) {
	runID := args[8].Value.(string)
	expected := args[9].Value.(int64)
	fence := args[10].Value.(int64)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	row, exists := c.state.rows[runID]
	if !exists || row.version != expected || row.fenceToken > fence {
		return driver.RowsAffected(0), nil
	}
	c.state.rows[runID] = testRow{version: args[0].Value.(int64), snapshot: valueBytes(args[7].Value), fenceToken: fence, updatedAt: time.Now().UTC()}
	return driver.RowsAffected(1), nil
}

// queryFence backs the classifyStaleSave follow-up read.
func (c *testConn) queryFence(args []driver.NamedValue) (driver.Rows, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	row, ok := c.state.rows[runID]
	c.state.mu.Unlock()
	if !ok {
		return &testRows{columns: []string{"version", "fence_token"}}, nil
	}
	return &testRows{
		columns: []string{"version", "fence_token"},
		values:  [][]driver.Value{{row.version, row.fenceToken}},
	}, nil
}

// queryStaleSnapshots backs ListStale: it honors the grace argument (the only
// argument the tests pass) and ignores any further filter clauses.
func (c *testConn) queryStaleSnapshots(args []driver.NamedValue) (driver.Rows, error) {
	graceSeconds := 0.0
	for _, arg := range args {
		switch value := arg.Value.(type) {
		case float64:
			graceSeconds = value
		case int64:
			graceSeconds = float64(value)
		}
	}
	cutoff := time.Now().UTC().Add(-time.Duration(graceSeconds * float64(time.Second)))
	c.state.mu.Lock()
	values := make([][]driver.Value, 0, len(c.state.rows))
	for _, row := range c.state.rows {
		if row.updatedAt.Before(cutoff) {
			values = append(values, []driver.Value{cloneBytes(row.snapshot)})
		}
	}
	c.state.mu.Unlock()
	return &testRows{columns: []string{"snapshot_json"}, values: values}, nil
}

func (c *testConn) delete(args []driver.NamedValue) (driver.Result, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	delete(c.state.rows, runID)
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}

// insertOutbox handles the outbox INSERT (run_id, sequence, event_type,
// scenario_name, payload_json), minting the bigserial id.
func (c *testConn) insertOutbox(args []driver.NamedValue) (driver.Result, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.outboxSeq++
	id := c.state.outboxSeq
	c.state.outbox[id] = testOutboxRow{
		id:           id,
		runID:        args[0].Value.(string),
		sequence:     args[1].Value.(int64),
		eventType:    args[2].Value.(string),
		scenarioName: args[3].Value.(string),
		payload:      valueBytes(args[4].Value),
		createdAt:    time.Now().UTC(),
	}
	return driver.RowsAffected(1), nil
}

// queryMaxEventSequence backs the GREATEST(max(events), max(outbox)) sequence
// minting query: the single argument is the run id.
func (c *testConn) queryMaxEventSequence(args []driver.NamedValue) (driver.Rows, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	maxSeq := c.state.eventSeq[runID]
	for _, row := range c.state.outbox {
		if row.runID == runID && row.sequence > maxSeq {
			maxSeq = row.sequence
		}
	}
	c.state.mu.Unlock()
	return &testRows{columns: []string{"greatest"}, values: [][]driver.Value{{maxSeq}}}, nil
}

// queryUnpublishedOutbox backs FetchUnpublishedOutbox.
func (c *testConn) queryUnpublishedOutbox(args []driver.NamedValue) (driver.Rows, error) {
	limit := 0
	if len(args) > 0 {
		limit = driverInt(args[0].Value)
	}
	c.state.mu.Lock()
	rows := make([]testOutboxRow, 0)
	for _, row := range c.state.outbox {
		if row.publishedAt.IsZero() {
			rows = append(rows, row)
		}
	}
	c.state.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	values := make([][]driver.Value, 0, len(rows))
	for _, row := range rows {
		values = append(values, []driver.Value{row.id, row.runID, row.sequence, cloneBytes(row.payload), row.createdAt})
	}
	return &testRows{columns: []string{"id", "run_id", "sequence", "payload_json", "created_at"}, values: values}, nil
}

// markOutboxPublished backs the conditional publish marking.
func (c *testConn) markOutboxPublished(args []driver.NamedValue) (driver.Result, error) {
	id := args[0].Value.(int64)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	row, ok := c.state.outbox[id]
	if !ok || !row.publishedAt.IsZero() {
		return driver.RowsAffected(0), nil
	}
	row.publishedAt = time.Now().UTC()
	c.state.outbox[id] = row
	return driver.RowsAffected(1), nil
}

// deleteOutboxForRun backs the retention cascade's per-run outbox delete.
func (c *testConn) deleteOutboxForRun(args []driver.NamedValue) (driver.Result, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	var removed int64
	for id, row := range c.state.outbox {
		if row.runID == runID {
			delete(c.state.outbox, id)
			removed++
		}
	}
	return driver.RowsAffected(removed), nil
}

// purgeOutboxPublished backs the age-based published-row cleanup.
func (c *testConn) purgeOutboxPublished(args []driver.NamedValue) (driver.Result, error) {
	cutoff := args[0].Value.(time.Time)
	tenantID := args[1].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	var removed int64
	for id, row := range c.state.outbox {
		if row.publishedAt.IsZero() || !row.publishedAt.Before(cutoff) {
			continue
		}
		if tenantID != "" {
			var event struct {
				TenantID string `json:"tenant_id"`
			}
			if err := json.Unmarshal(row.payload, &event); err != nil || event.TenantID != tenantID {
				continue
			}
		}
		delete(c.state.outbox, id)
		removed++
	}
	return driver.RowsAffected(removed), nil
}

type testRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *testRows) Columns() []string { return r.columns }

func (r *testRows) Close() error { return nil }

func (r *testRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func valueBytes(value any) []byte {
	switch typed := value.(type) {
	case []byte:
		return cloneBytes(typed)
	case string:
		return []byte(typed)
	default:
		return []byte(fmt.Sprint(typed))
	}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
