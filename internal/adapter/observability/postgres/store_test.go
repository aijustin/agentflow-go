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

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

const testDriverName = "agentflow_postgres_observability_test"

var (
	registerTestDriver sync.Once
	testDBSeq          atomic.Int64
	testStatesMu       sync.Mutex
	testStates         = make(map[string]*testState)
)

func TestNewStoreAutoMigratesSchema(t *testing.T) {
	db, state := openTestDB(t)
	store, err := NewStore(context.Background(), Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("expected store")
	}
	state.mu.Lock()
	execs := append([]string(nil), state.execs...)
	state.mu.Unlock()
	joined := strings.Join(execs, "\n")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS agentflow_runtime_events",
		"CREATE INDEX IF NOT EXISTS agentflow_runtime_events_run_sequence_idx",
		"CREATE INDEX IF NOT EXISTS agentflow_runtime_events_run_updated_idx",
		"CREATE INDEX IF NOT EXISTS agentflow_runtime_events_type_time_idx",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected auto migration query containing %q, got:\n%s", want, joined)
		}
	}
}

func TestNewStoreCanSkipSchemaSetup(t *testing.T) {
	db, state := openTestDB(t)
	if _, err := NewStore(context.Background(), Config{DB: db, SkipSchemaSetup: true}); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	execCount := len(state.execs)
	state.mu.Unlock()
	if execCount != 0 {
		t.Fatalf("expected no schema execs when skipped, got %d", execCount)
	}
}

func TestStoreAppendsListsRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	store, err := NewStore(ctx, Config{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	first, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "sales", Timestamp: base})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Append(ctx, core.Event{Type: core.EventToolCalled, RunID: "run-1", ScenarioName: "sales", Timestamp: base.Add(time.Second), Payload: json.RawMessage(`{"tool":"search"}`)})
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.Append(ctx, core.Event{Type: core.EventRunCompleted, RunID: "run-1", ScenarioName: "sales", Timestamp: base.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := store.Append(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-2", ScenarioName: "support", Timestamp: base.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 3 || fourth.Sequence != 1 {
		t.Fatalf("unexpected per-run sequences: %d %d %d %d", first.Sequence, second.Sequence, third.Sequence, fourth.Sequence)
	}

	events, err := store.ListEvents(ctx, "run-1", obspkg.EventQuery{AfterSequence: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Event.Type != core.EventToolCalled || string(events[0].Event.Payload) != `{"tool":"search"}` {
		t.Fatalf("unexpected events: %+v", events)
	}

	runs, err := store.ListRuns(ctx, obspkg.RunQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].RunID != "run-2" || runs[1].RunID != "run-1" {
		t.Fatalf("unexpected runs order: %+v", runs)
	}
	if runs[1].Status != obspkg.RunStatusCompleted || runs[1].EventCount != 3 {
		t.Fatalf("unexpected run-1 summary: %+v", runs[1])
	}

	completed, err := store.ListRuns(ctx, obspkg.RunQuery{Status: obspkg.RunStatusCompleted, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 1 || completed[0].RunID != "run-1" {
		t.Fatalf("expected completed run-1, got %+v", completed)
	}
}

func TestNewStoreValidatesConfig(t *testing.T) {
	if _, err := NewStore(context.Background(), Config{}); err == nil {
		t.Fatal("expected nil db error")
	}
	db, _ := openTestDB(t)
	if _, err := NewStore(context.Background(), Config{DB: db, TableName: "agentflow.runtime_events"}); err != nil {
		t.Fatalf("expected schema-qualified table to be accepted: %v", err)
	}
	if _, err := NewStore(context.Background(), Config{DB: db, TableName: "bad;drop"}); err == nil {
		t.Fatal("expected invalid table name error")
	}
}

func openTestDB(t *testing.T) (*sql.DB, *testState) {
	t.Helper()
	registerTestDriver.Do(func() { sql.Register(testDriverName, testDriver{}) })
	key := fmt.Sprintf("obs-%d", testDBSeq.Add(1))
	state := &testState{}
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
	mu     sync.Mutex
	execs  []string
	nextID int64
	rows   []testRow
}

type testRow struct {
	id           int64
	sequence     int64
	eventType    string
	runID        string
	scenarioName string
	traceID      string
	spanID       string
	parentSpanID string
	occurredAt   time.Time
	payload      []byte
	createdAt    time.Time
}

type testConn struct{ state *testState }

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c *testConn) Close() error              { return nil }
func (c *testConn) Begin() (driver.Tx, error) { return testTx{}, nil }
func (c *testConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return testTx{}, nil
}

type testTx struct{}

func (testTx) Commit() error   { return nil }
func (testTx) Rollback() error { return nil }

func (c *testConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.mu.Lock()
	c.state.execs = append(c.state.execs, strings.TrimSpace(query))
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}

func (c *testConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "SELECT COALESCE(MAX(SEQUENCE)"):
		return c.nextSequence(args)
	case strings.HasPrefix(normalized, "INSERT INTO"):
		return c.insertEvent(args)
	case strings.HasPrefix(normalized, "SELECT ID, SEQUENCE"):
		return c.listEvents(args)
	case strings.HasPrefix(normalized, "WITH SUMMARIZED"):
		return c.listRuns(args)
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

func (c *testConn) nextSequence(args []driver.NamedValue) (driver.Rows, error) {
	runID := namedString(args[0])
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	var maxSequence int64
	for _, row := range c.state.rows {
		if row.runID == runID && row.sequence > maxSequence {
			maxSequence = row.sequence
		}
	}
	return newTestRows([]string{"sequence"}, [][]driver.Value{{maxSequence + 1}}), nil
}

func (c *testConn) insertEvent(args []driver.NamedValue) (driver.Rows, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.nextID++
	createdAt := time.Date(2026, 5, 17, 12, 0, int(c.state.nextID), 0, time.UTC)
	row := testRow{
		id:           c.state.nextID,
		runID:        namedString(args[0]),
		sequence:     namedInt64(args[1].Value),
		eventType:    namedString(args[2]),
		scenarioName: namedString(args[3]),
		traceID:      namedString(args[4]),
		spanID:       namedString(args[5]),
		parentSpanID: namedString(args[6]),
		occurredAt:   args[7].Value.(time.Time),
		payload:      valueBytes(args[8].Value),
		createdAt:    createdAt,
	}
	c.state.rows = append(c.state.rows, row)
	return newTestRows([]string{"id", "created_at"}, [][]driver.Value{{row.id, createdAt}}), nil
}

func (c *testConn) listEvents(args []driver.NamedValue) (driver.Rows, error) {
	runID := namedString(args[0])
	afterSequence := namedInt64(args[1].Value)
	limit := int(namedInt64(args[2].Value))
	c.state.mu.Lock()
	rows := append([]testRow(nil), c.state.rows...)
	c.state.mu.Unlock()
	values := make([][]driver.Value, 0)
	for _, row := range rows {
		if row.runID != runID || row.sequence <= afterSequence {
			continue
		}
		values = append(values, row.values())
		if len(values) >= limit {
			break
		}
	}
	return newTestRows(eventColumns(), values), nil
}

func (c *testConn) listRuns(args []driver.NamedValue) (driver.Rows, error) {
	statusFilter := namedString(args[0])
	limit := int(namedInt64(args[1].Value))
	offset := int(namedInt64(args[2].Value))
	c.state.mu.Lock()
	rows := append([]testRow(nil), c.state.rows...)
	c.state.mu.Unlock()
	byRun := make(map[string]obspkg.RunSummary)
	for _, row := range rows {
		summary := byRun[row.runID]
		if summary.RunID == "" {
			summary.RunID = row.runID
			summary.FirstSeenAt = row.occurredAt
		}
		if summary.ScenarioName == "" {
			summary.ScenarioName = row.scenarioName
		}
		summary.EventCount++
		summary.LastSeenAt = row.occurredAt
		summary.LastEventType = core.EventType(row.eventType)
		summary.Status = obspkg.StatusAfterEvent(summary.Status, core.EventType(row.eventType))
		byRun[row.runID] = summary
	}
	summaries := make([]obspkg.RunSummary, 0, len(byRun))
	for _, summary := range byRun {
		if statusFilter != "" && string(summary.Status) != statusFilter {
			continue
		}
		summaries = append(summaries, summary)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].LastSeenAt.Equal(summaries[j].LastSeenAt) {
			return summaries[i].RunID < summaries[j].RunID
		}
		return summaries[i].LastSeenAt.After(summaries[j].LastSeenAt)
	})
	if offset >= len(summaries) {
		return newTestRows(runColumns(), nil), nil
	}
	summaries = summaries[offset:]
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	values := make([][]driver.Value, 0, len(summaries))
	for _, summary := range summaries {
		values = append(values, []driver.Value{summary.RunID, summary.ScenarioName, string(summary.Status), summary.EventCount, summary.FirstSeenAt, summary.LastSeenAt, string(summary.LastEventType)})
	}
	return newTestRows(runColumns(), values), nil
}

func (row testRow) values() []driver.Value {
	return []driver.Value{row.id, row.sequence, row.eventType, row.runID, row.scenarioName, row.traceID, row.spanID, row.parentSpanID, row.occurredAt, cloneBytes(row.payload), row.createdAt}
}

type testRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func newTestRows(columns []string, values [][]driver.Value) *testRows {
	return &testRows{columns: columns, values: values}
}

func (rows *testRows) Columns() []string { return rows.columns }
func (rows *testRows) Close() error      { return nil }
func (rows *testRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(dest, rows.values[rows.index])
	rows.index++
	return nil
}

func eventColumns() []string {
	return []string{"id", "sequence", "event_type", "run_id", "scenario_name", "trace_id", "span_id", "parent_span_id", "occurred_at", "payload_json", "created_at"}
}

func runColumns() []string {
	return []string{"run_id", "scenario_name", "status", "event_count", "first_seen_at", "last_seen_at", "last_event_type"}
}

func namedString(value driver.NamedValue) string {
	if value.Value == nil {
		return ""
	}
	return fmt.Sprint(value.Value)
}

func namedInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	default:
		return 0
	}
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
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}
