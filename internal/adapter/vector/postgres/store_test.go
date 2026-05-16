package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/knowledge"
)

const testDriverName = "agentflow_pgvector_store_test"

var (
	registerTestDriver sync.Once
	testDBSeq          atomic.Int64
	testStatesMu       sync.Mutex
	testStates         = make(map[string]*testState)
)

func TestStoreUpsertQueryAndDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	docs := []knowledge.DocumentEmbedding{{
		Document: knowledge.Document{ID: "doc-1", Namespace: "tenant-a", Content: "hello", Metadata: map[string]string{"source": "guide"}},
		Vector:   []float32{0.1, 0.2},
	}}
	if err := store.Upsert(ctx, docs); err != nil {
		t.Fatal(err)
	}
	results, err := store.Query(ctx, knowledge.Query{Namespace: "tenant-a", Vector: []float32{0.1, 0.2}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Document.ID != "doc-1" || results[0].Score <= 0 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if err := store.Delete(ctx, knowledge.DeleteRequest{Namespace: "tenant-a", ID: "doc-1"}); err != nil {
		t.Fatal(err)
	}
	results, err = store.Query(ctx, knowledge.Query{Namespace: "tenant-a", Vector: []float32{0.1, 0.2}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected deleted document to be gone, got %+v", results)
	}
}

func TestNewStoreValidatesInputs(t *testing.T) {
	if _, err := NewStore(nil); err == nil {
		t.Fatal("expected nil db error")
	}
	db := openTestDB(t)
	if _, err := NewStore(db, WithTableName("knowledge.embeddings")); err != nil {
		t.Fatalf("expected schema-qualified table to be accepted: %v", err)
	}
	if _, err := NewStore(db, WithTableName("bad;drop")); err == nil {
		t.Fatal("expected invalid table name error")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	registerTestDriver.Do(func() { sql.Register(testDriverName, testDriver{}) })
	key := fmt.Sprintf("vector-%d", testDBSeq.Add(1))
	testStatesMu.Lock()
	testStates[key] = &testState{rows: make(map[string]testRow)}
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
	mu   sync.Mutex
	rows map[string]testRow
}

type testRow struct {
	namespace string
	id        string
	content   string
	metadata  []byte
	vector    string
}

type testConn struct{ state *testState }

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c *testConn) Close() error { return nil }
func (c *testConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *testConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "INSERT INTO"):
		return c.upsert(args)
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
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "SELECT") {
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
	namespace := args[0].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	values := make([][]driver.Value, 0)
	for _, row := range c.state.rows {
		if row.namespace != namespace {
			continue
		}
		values = append(values, []driver.Value{row.id, row.content, row.metadata, float64(0.99)})
	}
	return &testRows{values: values}, nil
}

func (c *testConn) upsert(args []driver.NamedValue) (driver.Result, error) {
	row := testRow{namespace: args[0].Value.(string), id: args[1].Value.(string), content: args[2].Value.(string), metadata: bytesValue(args[3].Value), vector: args[4].Value.(string)}
	c.state.mu.Lock()
	c.state.rows[row.namespace+"/"+row.id] = row
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}

func (c *testConn) delete(args []driver.NamedValue) (driver.Result, error) {
	namespace := args[0].Value.(string)
	id := args[1].Value.(string)
	c.state.mu.Lock()
	delete(c.state.rows, namespace+"/"+id)
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}

type testRows struct {
	values [][]driver.Value
	index  int
}

func (r *testRows) Columns() []string {
	return []string{"document_id", "content", "metadata_json", "score"}
}
func (r *testRows) Close() error { return nil }
func (r *testRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func bytesValue(value any) []byte {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...)
	case string:
		return []byte(typed)
	default:
		data, _ := json.Marshal(typed)
		return data
	}
}
