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
	registerTestDriver.Do(func() {
		sql.Register(testDriverName, testDriver{})
	})
	key := fmt.Sprintf("state-%d", testDBSeq.Add(1))
	testStatesMu.Lock()
	testStates[key] = &testState{
		rows:        make(map[string]testRow),
		checkpoints: make(map[string]testCheckpointRow),
	}
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
	return db
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
	mu           sync.Mutex
	rows         map[string]testRow
	checkpoints  map[string]testCheckpointRow
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
	version  int64
	snapshot []byte
}

type testConn struct {
	state *testState
}

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by test driver")
}

func (c *testConn) Close() error { return nil }

func (c *testConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by test driver")
}

func (c *testConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "INSERT INTO") && strings.Contains(normalized, "CHECKPOINT"):
		return c.insertCheckpoint(args)
	case strings.HasPrefix(normalized, "INSERT INTO"):
		return c.insert(args)
	case strings.HasPrefix(normalized, "UPDATE"):
		return c.update(args)
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
	case strings.HasPrefix(normalized, "SELECT SNAPSHOT_JSON FROM"):
		if strings.Contains(normalized, "CHECKPOINT") {
			return c.queryCheckpointSnapshot(args)
		}
		return c.queryRunSnapshot(args)
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
	c.state.rows[runID] = testRow{version: args[1].Value.(int64), snapshot: valueBytes(args[8].Value)}
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
	c.state.rows[runID] = testRow{version: args[0].Value.(int64), snapshot: valueBytes(args[7].Value)}
	return driver.RowsAffected(1), nil
}

func (c *testConn) delete(args []driver.NamedValue) (driver.Result, error) {
	runID := args[0].Value.(string)
	c.state.mu.Lock()
	delete(c.state.rows, runID)
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
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
