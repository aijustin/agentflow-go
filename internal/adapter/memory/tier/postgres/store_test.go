package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

// --- Fake SQL driver (mirrors the runstate/queue test-driver pattern) ---

const tierTestDriverName = "agentflow_tier_postgres_test"

var (
	registerTierTestDriver sync.Once
	tierTestDBSeq          int
	tierTestStatesMu       sync.Mutex
	tierTestStates         = map[string]*tierTestState{}
)

type tierTestState struct {
	mu   sync.Mutex
	rows map[string]tierTestRow
}

type tierTestRow struct {
	ns         string
	id         string
	level      string
	lastAccess time.Time
	recordJSON []byte
}

func openTierTestDB(t *testing.T) (*sql.DB, *tierTestState) {
	t.Helper()
	registerTierTestDriver.Do(func() {
		sql.Register(tierTestDriverName, tierTestDriver{})
	})
	tierTestDBSeq++
	key := fmt.Sprintf("tier-%d", tierTestDBSeq)
	state := &tierTestState{rows: make(map[string]tierTestRow)}
	tierTestStatesMu.Lock()
	tierTestStates[key] = state
	tierTestStatesMu.Unlock()
	db, err := sql.Open(tierTestDriverName, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		tierTestStatesMu.Lock()
		delete(tierTestStates, key)
		tierTestStatesMu.Unlock()
	})
	return db, state
}

type tierTestDriver struct{}

func (tierTestDriver) Open(name string) (driver.Conn, error) {
	tierTestStatesMu.Lock()
	state := tierTestStates[name]
	tierTestStatesMu.Unlock()
	if state == nil {
		return nil, fmt.Errorf("unknown test database %q", name)
	}
	return &tierTestConn{state: state}, nil
}

type tierTestConn struct{ state *tierTestState }

func (c *tierTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepare unsupported")
}
func (c *tierTestConn) Close() error              { return nil }
func (c *tierTestConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("transactions unsupported") }

func (c *tierTestConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "INSERT INTO"):
		ns := args[0].Value.(string)
		id := args[1].Value.(string)
		level := args[2].Value.(string)
		lastAccess := args[3].Value.(time.Time)
		var raw []byte
		switch v := args[4].Value.(type) {
		case []byte:
			raw = v
		case string:
			raw = []byte(v)
		}
		c.state.mu.Lock()
		c.state.rows[ns+"/"+id] = tierTestRow{ns: ns, id: id, level: level, lastAccess: lastAccess, recordJSON: raw}
		c.state.mu.Unlock()
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(normalized, "DELETE FROM"):
		ns := args[0].Value.(string)
		id := args[1].Value.(string)
		level := args[2].Value.(string)
		c.state.mu.Lock()
		defer c.state.mu.Unlock()
		row, ok := c.state.rows[ns+"/"+id]
		if !ok || row.level != level {
			return driver.RowsAffected(0), nil
		}
		delete(c.state.rows, ns+"/"+id)
		return driver.RowsAffected(1), nil
	default:
		return nil, fmt.Errorf("unsupported exec: %s", query)
	}
}

func (c *tierTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized := strings.ToUpper(strings.TrimSpace(query))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	switch {
	case strings.HasPrefix(normalized, "SELECT COUNT(*)"):
		ns := args[0].Value.(string)
		level := args[1].Value.(string)
		count := int64(0)
		for _, row := range c.state.rows {
			if row.ns == ns && row.level == level {
				count++
			}
		}
		return &tierTestRows{columns: []string{"count"}, values: [][]driver.Value{{count}}}, nil
	case strings.Contains(normalized, "RECORD_ID"):
		ns := args[0].Value.(string)
		id := args[1].Value.(string)
		level := args[2].Value.(string)
		row, ok := c.state.rows[ns+"/"+id]
		if !ok || row.level != level {
			return &tierTestRows{columns: []string{"record_json"}}, nil
		}
		return &tierTestRows{columns: []string{"record_json"}, values: [][]driver.Value{{row.recordJSON}}}, nil
	case strings.Contains(normalized, "ORDER BY LAST_ACCESS_AT DESC"):
		ns := args[0].Value.(string)
		level := args[1].Value.(string)
		matched := make([]tierTestRow, 0)
		for _, row := range c.state.rows {
			if row.ns == ns && row.level == level {
				matched = append(matched, row)
			}
		}
		for i := 0; i < len(matched); i++ {
			for j := i + 1; j < len(matched); j++ {
				if matched[j].lastAccess.After(matched[i].lastAccess) {
					matched[i], matched[j] = matched[j], matched[i]
				}
			}
		}
		if len(args) > 2 {
			if limit := int(args[2].Value.(int64)); limit < len(matched) {
				matched = matched[:limit]
			}
		}
		values := make([][]driver.Value, 0, len(matched))
		for _, row := range matched {
			values = append(values, []driver.Value{row.recordJSON})
		}
		return &tierTestRows{columns: []string{"record_json"}, values: values}, nil
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

type tierTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *tierTestRows) Columns() []string { return r.columns }
func (r *tierTestRows) Close() error      { return nil }
func (r *tierTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

// --- Tests ---

func testNS() memory.Namespace {
	return memory.Namespace{Scope: memory.ScopeSession, SessionID: "sess-1", Agent: "assistant"}
}

// TestSingleLevelStorePersistsAcrossManagers proves the single-level form: a
// warm-level Postgres store used alone as tier.Store (no composite, no job
// queue) persists Remembered records durably, so a brand-new manager over
// the same database recalls them — the "process restart" guarantee a host
// needs for session memory.
func TestSingleLevelStorePersistsAcrossManagers(t *testing.T) {
	db, _ := openTierTestDB(t)
	ctx := context.Background()
	ns := testNS()

	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	manager := tier.NewManager(store, tier.SingleLevelPolicy(), nil)
	msgTime := time.Now().UTC().Add(-time.Hour)
	record, err := tier.MessageRecord(ns, tier.ChatMessage{
		Role: "user", Content: "remember this", Time: msgTime,
	}, tier.WithProvenance(memory.ProvenanceIntegrator))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Remember(ctx, ns, record); err != nil {
		t.Fatal(err)
	}

	// "Restart": a fresh store + manager over the same database.
	restarted, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	restartedManager := tier.NewManager(restarted, tier.SingleLevelPolicy(), nil)
	recalled, err := restartedManager.Recall(ctx, ns, "", tier.RecallBudget{Total: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 {
		t.Fatalf("expected the seeded record to survive the restart, got %d records", len(recalled))
	}
	if recalled[0].Tier != tier.LevelWarm {
		t.Fatalf("warm-level store must force its own level, got %q", recalled[0].Tier)
	}
	if !strings.Contains(recalled[0].Content, "remember this") {
		t.Fatalf("unexpected record content: %q", recalled[0].Content)
	}
}

// TestSingleLevelStoreServesOnlyItsLevel pins the store contract that makes
// the single-level form work: reads for other levels are empty, never an
// error, so manager recall walks hot/cold and simply finds nothing there.
func TestSingleLevelStoreServesOnlyItsLevel(t *testing.T) {
	db, _ := openTierTestDB(t)
	ctx := context.Background()
	ns := testNS()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	record, err := tier.MessageRecord(ns, tier.ChatMessage{Role: "user", Content: "x", Time: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	for _, level := range []tier.Level{tier.LevelHot, tier.LevelCold} {
		records, err := store.List(ctx, ns, level, 0)
		if err != nil {
			t.Fatalf("List(%s) must not error, got %v", level, err)
		}
		if len(records) != 0 {
			t.Fatalf("List(%s) must be empty for a warm-level store, got %d", level, len(records))
		}
		count, err := store.Count(ctx, ns, level)
		if err != nil || count != 0 {
			t.Fatalf("Count(%s) = %d, %v; want 0, nil", level, count, err)
		}
	}
	warm, err := store.List(ctx, ns, tier.LevelWarm, 0)
	if err != nil || len(warm) != 1 {
		t.Fatalf("expected one warm record, got %d, %v", len(warm), err)
	}
}

// TestSingleLevelPolicyNeverMigrates pins the policy half of the form:
// recall-time bookkeeping must not promote/demote records out of the
// durable tier even after repeated accesses.
func TestSingleLevelPolicyNeverMigrates(t *testing.T) {
	policy := tier.SingleLevelPolicy()
	record := tier.Record{Tier: tier.LevelWarm, AccessCount: 100, LastAccessAt: time.Now().UTC().Add(-365 * 24 * time.Hour)}
	now := time.Now().UTC()
	if policy.ShouldPromote(record, now) {
		t.Fatal("single-level policy must never promote")
	}
	if policy.ShouldDemote(record, now, 100000) {
		t.Fatal("single-level policy must never demote")
	}
	if got := policy.TargetTier(record, now, map[tier.Level]int{tier.LevelWarm: 100000}); got != tier.LevelWarm {
		t.Fatalf("target tier must stay warm, got %q", got)
	}
}
