package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

const testDriverName = "agentflow_postgres_queue_test"

var (
	registerTestDriver sync.Once
	testDBSeq          atomic.Int64
	testStatesMu       sync.Mutex
	testStates         = make(map[string]*testState)
)

func TestQueueLeasesAndCompletesJobs(t *testing.T) {
	ctx := context.Background()
	queue := newTestQueue(t)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	job, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, Payload: []byte(`{"prompt":"hi"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != asyncpkg.JobQueued || job.MaxAttempts != 1 {
		t.Fatalf("unexpected enqueued job: %+v", job)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	if lease.JobID != "job-1" || lease.Attempt != 1 || lease.WorkerID != "worker-1" {
		t.Fatalf("unexpected lease: %+v", lease)
	}
	if _, ok, err := queue.Lease(ctx, "worker-2", time.Minute); err != nil || ok {
		t.Fatalf("expected no second lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Complete(ctx, lease); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobCompleted || loaded.LeaseWorkerID != "" {
		t.Fatalf("unexpected completed job: %+v", loaded)
	}
}

func TestQueueRetriesUntilDeadLetter(t *testing.T) {
	ctx := context.Background()
	queue := newTestQueue(t)
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected first lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobQueued || loaded.LastError != "boom" {
		t.Fatalf("expected queued retry, got %+v", loaded)
	}
	lease, ok, err = queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected second lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom again")); err != nil {
		t.Fatal(err)
	}
	loaded, err = queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobDeadLetter || loaded.LastError != "boom again" {
		t.Fatalf("expected dead letter, got %+v", loaded)
	}
}

func TestQueueRecoversExpiredLeases(t *testing.T) {
	ctx := context.Background()
	queue := newTestQueue(t)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	now = now.Add(2 * time.Minute)
	recovered, ok, err := queue.Lease(ctx, "worker-2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected recovered lease, ok=%v err=%v", ok, err)
	}
	if recovered.Attempt != lease.Attempt+1 || recovered.WorkerID != "worker-2" {
		t.Fatalf("unexpected recovered lease: %+v", recovered)
	}
	if err := queue.Complete(ctx, lease); !errors.Is(err, asyncpkg.ErrStaleLease) {
		t.Fatalf("expected stale original lease, got %v", err)
	}
}

func TestQueueCancelsJobsAndLoadsMissing(t *testing.T) {
	ctx := context.Background()
	queue := newTestQueue(t)
	if _, err := queue.Load(ctx, "missing"); !errors.Is(err, asyncpkg.ErrJobNotFound) {
		t.Fatalf("expected missing job, got %v", err)
	}
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Cancel(ctx, "job-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobCancelled {
		t.Fatalf("expected cancelled, got %+v", loaded)
	}
	if err := queue.Cancel(ctx, "missing"); !errors.Is(err, asyncpkg.ErrJobNotFound) {
		t.Fatalf("expected missing cancel error, got %v", err)
	}
}

func TestNewQueueValidatesInputs(t *testing.T) {
	if _, err := NewQueue(nil); err == nil {
		t.Fatal("expected nil db error")
	}
	db := openTestDB(t)
	if _, err := NewQueue(db, WithTableName("agentflow.jobs")); err != nil {
		t.Fatalf("expected schema-qualified table to be accepted: %v", err)
	}
	if _, err := NewQueue(db, WithTableName("bad;drop")); err == nil {
		t.Fatal("expected invalid table name error")
	}
}

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	db := openTestDB(t)
	queue, err := NewQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	registerTestDriver.Do(func() { sql.Register(testDriverName, testDriver{}) })
	key := fmt.Sprintf("queue-%d", testDBSeq.Add(1))
	testStatesMu.Lock()
	testStates[key] = &testState{rows: make(map[string]asyncpkg.Job)}
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
	mu    sync.Mutex
	rows  map[string]asyncpkg.Job
	order []string
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
		return c.insert(args)
	case strings.Contains(normalized, "CASE WHEN ATTEMPTS >= MAX_ATTEMPTS"):
		return c.fail(args)
	case strings.Contains(normalized, "STATE NOT IN"):
		return c.cancel(args)
	case strings.Contains(normalized, "STATE = $1") && strings.Contains(normalized, "LEASE_WORKER_ID = NULL"):
		return c.complete(args)
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
	case strings.HasPrefix(normalized, "UPDATE") && strings.Contains(normalized, "RETURNING"):
		return c.lease(args)
	case strings.HasPrefix(normalized, "SELECT"):
		return c.load(args)
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

func (c *testConn) insert(args []driver.NamedValue) (driver.Result, error) {
	job := asyncpkg.Job{
		ID:             args[0].Value.(string),
		Type:           args[1].Value.(string),
		RunID:          stringValue(args[2].Value),
		Payload:        bytesValue(args[3].Value),
		State:          asyncpkg.JobState(args[4].Value.(string)),
		Attempts:       int(args[5].Value.(int64)),
		MaxAttempts:    int(args[6].Value.(int64)),
		LastError:      stringValue(args[7].Value),
		CreatedAt:      args[8].Value.(time.Time),
		UpdatedAt:      args[9].Value.(time.Time),
		AvailableAt:    args[10].Value.(time.Time),
		LeaseWorkerID:  stringValue(args[11].Value),
		LeaseExpiresAt: timeValue(args[12].Value),
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if _, exists := c.state.rows[job.ID]; exists {
		return driver.RowsAffected(0), nil
	}
	c.state.rows[job.ID] = job
	c.state.order = append(c.state.order, job.ID)
	return driver.RowsAffected(1), nil
}

func (c *testConn) lease(args []driver.NamedValue) (driver.Rows, error) {
	running := asyncpkg.JobState(args[0].Value.(string))
	workerID := args[1].Value.(string)
	expires := args[2].Value.(time.Time)
	now := args[3].Value.(time.Time)
	queued := asyncpkg.JobState(args[4].Value.(string))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	for _, jobID := range c.state.order {
		job := c.state.rows[jobID]
		leaseable := job.State == queued && !job.AvailableAt.After(now)
		leaseable = leaseable || job.State == running && job.LeaseExpiresAt.Before(now)
		if !leaseable {
			continue
		}
		job.State = running
		job.Attempts++
		job.LeaseWorkerID = workerID
		job.LeaseExpiresAt = expires
		job.UpdatedAt = now
		c.state.rows[job.ID] = job
		return rows([][]driver.Value{jobValues(job)}), nil
	}
	return rows(nil), nil
}

func (c *testConn) load(args []driver.NamedValue) (driver.Rows, error) {
	jobID := args[0].Value.(string)
	c.state.mu.Lock()
	job, ok := c.state.rows[jobID]
	c.state.mu.Unlock()
	if !ok {
		return rows(nil), nil
	}
	return rows([][]driver.Value{jobValues(job)}), nil
}

func (c *testConn) complete(args []driver.NamedValue) (driver.Result, error) {
	state := asyncpkg.JobState(args[0].Value.(string))
	now := args[1].Value.(time.Time)
	jobID := args[2].Value.(string)
	workerID := args[4].Value.(string)
	attempt := int(args[5].Value.(int64))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != asyncpkg.JobRunning || job.LeaseWorkerID != workerID || job.Attempts != attempt {
		return driver.RowsAffected(0), nil
	}
	job.State = state
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = now
	c.state.rows[jobID] = job
	return driver.RowsAffected(1), nil
}

func (c *testConn) fail(args []driver.NamedValue) (driver.Result, error) {
	dead := asyncpkg.JobState(args[0].Value.(string))
	queued := asyncpkg.JobState(args[1].Value.(string))
	cause := stringValue(args[2].Value)
	now := args[3].Value.(time.Time)
	jobID := args[4].Value.(string)
	workerID := args[6].Value.(string)
	attempt := int(args[7].Value.(int64))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != asyncpkg.JobRunning || job.LeaseWorkerID != workerID || job.Attempts != attempt {
		return driver.RowsAffected(0), nil
	}
	if job.Attempts >= job.MaxAttempts {
		job.State = dead
	} else {
		job.State = queued
		job.AvailableAt = now
	}
	job.LastError = cause
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = now
	c.state.rows[jobID] = job
	return driver.RowsAffected(1), nil
}

func (c *testConn) cancel(args []driver.NamedValue) (driver.Result, error) {
	state := asyncpkg.JobState(args[0].Value.(string))
	now := args[1].Value.(time.Time)
	jobID := args[2].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State == asyncpkg.JobCompleted || job.State == asyncpkg.JobDeadLetter {
		return driver.RowsAffected(0), nil
	}
	job.State = state
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = now
	c.state.rows[jobID] = job
	return driver.RowsAffected(1), nil
}

func jobValues(job asyncpkg.Job) []driver.Value {
	return []driver.Value{job.ID, job.Type, job.RunID, []byte(job.Payload), string(job.State), int64(job.Attempts), int64(job.MaxAttempts), job.LastError, job.CreatedAt, job.UpdatedAt, job.AvailableAt, nullableString(job.LeaseWorkerID), nullableTime(job.LeaseExpiresAt)}
}

func rows(values [][]driver.Value) driver.Rows {
	return &testRows{columns: []string{"id", "type", "run_id", "payload_json", "state", "attempts", "max_attempts", "last_error", "created_at", "updated_at", "available_at", "lease_worker_id", "lease_expires_at"}, values: values}
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

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func bytesValue(value any) []byte {
	switch typed := value.(type) {
	case []byte:
		out := make([]byte, len(typed))
		copy(out, typed)
		return out
	case string:
		return []byte(typed)
	default:
		return []byte(fmt.Sprint(typed))
	}
}

func timeValue(value any) time.Time {
	if typed, ok := value.(time.Time); ok {
		return typed
	}
	return time.Time{}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
