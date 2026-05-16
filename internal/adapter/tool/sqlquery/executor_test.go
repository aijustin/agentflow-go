package sqlquery

import (
	"context"
	dbsql "database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

const testDriverName = "agentflow_sql_tool_test"

var (
	registerTestDriver sync.Once
	testDBSeq          atomic.Int64
	testStatesMu       sync.Mutex
	testStates         = make(map[string]*testState)
)

func TestExecutorRunsAllowlistedSelectAndReturnsRows(t *testing.T) {
	state := &testState{columns: []string{"id", "name"}, rows: [][]driver.Value{{int64(1), "alice"}}}
	db := openTestDB(t, state)
	executor, err := NewExecutor(Config{DB: db, AllowedQueries: map[string]string{"users.by_id": "SELECT id, name FROM users WHERE id = $1"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "sql.query", Input: mustInput(t, Request{QueryID: "users.by_id", Args: []any{float64(1)}})})
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.QueryID != "users.by_id" || out.RowCount != 1 || out.Truncated || out.Columns[1] != "name" || out.Rows[0]["name"] != "alice" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if state.lastQuery != "SELECT id, name FROM users WHERE id = $1" || len(state.lastArgs) != 1 || state.lastArgs[0] != float64(1) {
		t.Fatalf("unexpected query call: query=%q args=%v", state.lastQuery, state.lastArgs)
	}
}

func TestNewExecutorRejectsMutatingAllowedQuery(t *testing.T) {
	db := openTestDB(t, &testState{})
	_, err := NewExecutor(Config{DB: db, AllowedQueries: map[string]string{"users.delete": "DELETE FROM users WHERE id = $1"}})
	if err == nil {
		t.Fatal("expected mutating query rejection")
	}
}

func TestNewExecutorAcceptsMySQLAndClickHouseReadOnlyQueries(t *testing.T) {
	db := openTestDB(t, &testState{})
	_, err := NewExecutor(Config{DB: db, AllowedQueries: map[string]string{
		"mysql.orders":      "SELECT `order`, `delete` FROM `tickets` WHERE `status` = ? # comment with UPDATE\nORDER BY `created_at` DESC",
		"clickhouse.events": "WITH toStartOfHour(ts) AS bucket SELECT bucket, count() AS total FROM events WHERE ts >= ? GROUP BY bucket ORDER BY bucket DESC LIMIT ?",
	}})
	if err != nil {
		t.Fatalf("expected MySQL and ClickHouse read-only queries to be accepted: %v", err)
	}
}

func TestNewExecutorRejectsMutatingCTE(t *testing.T) {
	db := openTestDB(t, &testState{})
	_, err := NewExecutor(Config{DB: db, AllowedQueries: map[string]string{"bad.cte": "WITH moved AS (DELETE FROM users RETURNING id) SELECT id FROM moved"}})
	if err == nil {
		t.Fatal("expected mutating CTE rejection")
	}
}

func TestExecutorRejectsAdHocQueryByDefault(t *testing.T) {
	db := openTestDB(t, &testState{})
	executor, err := NewExecutor(Config{DB: db, AllowedQueries: map[string]string{"users.list": "SELECT id FROM users"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), core.ToolCall{Tool: "sql.query", Input: mustInput(t, Request{Query: "SELECT id FROM users"})})
	if err == nil {
		t.Fatal("expected ad-hoc query rejection")
	}
}

func TestExecutorLimitsRowsAndReportsTruncation(t *testing.T) {
	state := &testState{columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}, {int64(2)}}}
	db := openTestDB(t, state)
	executor, err := NewExecutor(Config{DB: db, AllowedQueries: map[string]string{"users.list": "SELECT id FROM users"}, MaxRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), core.ToolCall{Tool: "sql.query", Input: mustInput(t, Request{QueryID: "users.list"})})
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.RowCount != 1 || !out.Truncated || len(out.Rows) != 1 {
		t.Fatalf("expected one truncated row, got %+v", out)
	}
}

func openTestDB(t *testing.T, state *testState) *dbsql.DB {
	t.Helper()
	registerTestDriver.Do(func() { dbsql.Register(testDriverName, testDriver{}) })
	key := fmt.Sprintf("sql-tool-%d", testDBSeq.Add(1))
	testStatesMu.Lock()
	testStates[key] = state
	testStatesMu.Unlock()
	db, err := dbsql.Open(testDriverName, key)
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
	mu        sync.Mutex
	columns   []string
	rows      [][]driver.Value
	lastQuery string
	lastArgs  []any
}

type testConn struct{ state *testState }

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c *testConn) Close() error { return nil }
func (c *testConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *testConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.mu.Lock()
	c.state.lastQuery = query
	c.state.lastArgs = c.state.lastArgs[:0]
	for _, arg := range args {
		c.state.lastArgs = append(c.state.lastArgs, arg.Value)
	}
	columns := append([]string(nil), c.state.columns...)
	values := make([][]driver.Value, len(c.state.rows))
	for i := range c.state.rows {
		values[i] = append([]driver.Value(nil), c.state.rows[i]...)
	}
	c.state.mu.Unlock()
	return &testRows{columns: columns, values: values}, nil
}

type testRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *testRows) Columns() []string { return r.columns }
func (r *testRows) Close() error      { return nil }
func (r *testRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func mustInput(t *testing.T, input Request) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
