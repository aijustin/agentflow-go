package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
	mu    sync.Mutex
	execs []string
}

type testConn struct{ state *testState }

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c *testConn) Close() error { return nil }
func (c *testConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *testConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.mu.Lock()
	c.state.execs = append(c.state.execs, strings.TrimSpace(query))
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}
