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
	queue, _ := newTestQueue(t)
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
	queue, state := newTestQueue(t)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	state.setNow(now)
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
	// The retry backs off before becoming available again, so an immediate
	// re-lease finds nothing until the backoff window has passed.
	if _, ok, err := queue.Lease(ctx, "worker-1", time.Minute); err != nil || ok {
		t.Fatalf("expected retry backoff to block immediate re-lease, ok=%v err=%v", ok, err)
	}
	state.setNow(now.Add(2 * time.Second))
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
	queue, state := newTestQueue(t)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	state.setNow(now)
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	state.setNow(now.Add(2 * time.Minute))
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

func TestQueueRenewsLeases(t *testing.T) {
	ctx := context.Background()
	queue, state := newTestQueue(t)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	state.setNow(now)
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	now = now.Add(30 * time.Second)
	state.setNow(now)
	renewed, ok, err := queue.Renew(ctx, lease, 2*time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected renewal, ok=%v err=%v", ok, err)
	}
	if !renewed.ExpiresAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("unexpected renewed lease: %+v", renewed)
	}
	state.setNow(now.Add(90 * time.Second))
	if _, ok, err := queue.Lease(ctx, "worker-2", time.Minute); err != nil || ok {
		t.Fatalf("renewed lease should not be stolen, ok=%v err=%v", ok, err)
	}
}

func TestQueueRenewRejectsStaleLease(t *testing.T) {
	ctx := context.Background()
	queue, _ := newTestQueue(t)
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	stale := lease
	stale.WorkerID = "worker-2"
	if _, ok, err := queue.Renew(ctx, stale, time.Minute); !errors.Is(err, asyncpkg.ErrStaleLease) || ok {
		t.Fatalf("expected stale renewal rejection, ok=%v err=%v", ok, err)
	}
}

func TestQueueCancelsJobsAndLoadsMissing(t *testing.T) {
	ctx := context.Background()
	queue, _ := newTestQueue(t)
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

// TestQueueUsesDatabaseClockForLeaseDecisions pins the multi-node clock
// invariant: every availability or lease-expiry decision happens inside SQL
// against the database clock (NOW()), and no UPDATE carries an
// application-generated timestamp.
func TestQueueUsesDatabaseClockForLeaseDecisions(t *testing.T) {
	ctx := context.Background()
	queue, state := newTestQueue(t)
	base := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	state.setNow(base)
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-2", Type: asyncpkg.RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	renewed, ok, err := queue.Renew(ctx, lease, time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected renewal, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, renewed, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	// The failed retry backs off by one second; advance the database clock
	// past the backoff so the job can be leased again.
	state.setNow(base.Add(2 * time.Second))
	lease, ok, err = queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected re-lease after backoff, ok=%v err=%v", ok, err)
	}
	if err := queue.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
	lease, ok, err = queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease after release, ok=%v err=%v", ok, err)
	}
	if err := queue.Pause(ctx, lease, asyncpkg.PauseResult{RunID: "run-1", Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	// Drive job-2 to dead letter, then requeue it.
	lease, ok, err = queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease for job-2, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if err := queue.Requeue(ctx, "job-2"); err != nil {
		t.Fatal(err)
	}
	if err := queue.Cancel(ctx, "job-2"); err != nil {
		t.Fatal(err)
	}
	for _, rec := range state.recorded() {
		normalized := strings.ToUpper(strings.TrimSpace(rec.query))
		switch {
		case strings.HasPrefix(normalized, "INSERT"):
			if !strings.Contains(normalized, "NOW() + MAKE_INTERVAL") {
				t.Errorf("insert must anchor available_at at the database clock: %s", rec.query)
			}
		case strings.HasPrefix(normalized, "UPDATE"):
			if !strings.Contains(normalized, "NOW()") {
				t.Errorf("update must use the database clock: %s", rec.query)
			}
			for _, arg := range rec.args {
				if _, isTime := arg.Value.(time.Time); isTime {
					t.Errorf("update must not carry application-generated timestamps: %s (arg %v)", rec.query, arg.Value)
				}
			}
		}
	}
}

// TestQueueEnqueueAnchorsDelayedAvailabilityAtDatabaseClock verifies the
// AvailableAt delay semantics match the inmem queue: a future timestamp
// delays leasing by the remaining time, a past or zero timestamp makes the
// job available immediately — with the database clock as the anchor.
func TestQueueEnqueueAnchorsDelayedAvailabilityAtDatabaseClock(t *testing.T) {
	ctx := context.Background()
	queue, state := newTestQueue(t)
	base := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	state.setNow(base)
	// The application clock only converts the future timestamp into a
	// relative delay; the database re-anchors it at its own NOW().
	queue.now = func() time.Time { return base }
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-delayed", Type: asyncpkg.RunJobType, AvailableAt: base.Add(90 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-past", Type: asyncpkg.RunJobType, AvailableAt: base.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-delayed")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.AvailableAt.Equal(base.Add(90 * time.Second)) {
		t.Fatalf("expected available_at anchored at db now + delay, got %v", loaded.AvailableAt)
	}
	// The past-dated job is available immediately, the delayed one is not.
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok || lease.JobID != "job-past" {
		t.Fatalf("expected the past-dated job to be leaseable, ok=%v lease=%+v err=%v", ok, lease, err)
	}
	if err := queue.Complete(ctx, lease); err != nil {
		t.Fatal(err)
	}
	// Advance the database clock past the delay: the job becomes leaseable.
	state.setNow(base.Add(91 * time.Second))
	lease, ok, err = queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok || lease.JobID != "job-delayed" {
		t.Fatalf("expected delayed job after the delay elapsed, ok=%v lease=%+v err=%v", ok, lease, err)
	}
}

func TestNewQueueValidatesInputs(t *testing.T) {
	if _, err := NewQueue(nil); err == nil {
		t.Fatal("expected nil db error")
	}
	db, _ := openTestDB(t)
	if _, err := NewQueue(db, WithTableName("agentflow.jobs")); err != nil {
		t.Fatalf("expected schema-qualified table to be accepted: %v", err)
	}
	if _, err := NewQueue(db, WithTableName("bad;drop")); err == nil {
		t.Fatal("expected invalid table name error")
	}
}

func newTestQueue(t *testing.T) (*Queue, *testState) {
	t.Helper()
	db, state := openTestDB(t)
	queue, err := NewQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	return queue, state
}

func openTestDB(t *testing.T) (*sql.DB, *testState) {
	t.Helper()
	registerTestDriver.Do(func() { sql.Register(testDriverName, testDriver{}) })
	key := fmt.Sprintf("queue-%d", testDBSeq.Add(1))
	state := &testState{rows: make(map[string]asyncpkg.Job)}
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

// testState emulates a PostgreSQL server: it keeps its own clock so tests
// can advance the database time independently, and it records every query
// with its arguments so tests can assert that lease decisions are made by
// the database clock (NOW()) rather than by application-generated
// timestamps.
type testState struct {
	mu      sync.Mutex
	rows    map[string]asyncpkg.Job
	order   []string
	now     time.Time // emulated database clock; zero falls back to the wall clock
	queries []recordedQuery
}

type recordedQuery struct {
	query string
	args  []driver.NamedValue
}

// dbNow emulates the server-side NOW(). Callers must hold s.mu.
func (s *testState) dbNow() time.Time {
	if s.now.IsZero() {
		return time.Now().UTC()
	}
	return s.now
}

func (s *testState) setNow(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *testState) record(query string, args []driver.NamedValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, recordedQuery{query: query, args: args})
}

func (s *testState) recorded() []recordedQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedQuery, len(s.queries))
	copy(out, s.queries)
	return out
}

// secondsToDuration converts the float64 seconds carried by make_interval
// parameters back into a duration.
func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
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
	c.state.record(query, args)
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "INSERT INTO"):
		return c.insert(args)
	case strings.Contains(normalized, "CASE WHEN ATTEMPTS >= MAX_ATTEMPTS"):
		return c.fail(args)
	case strings.Contains(normalized, "STATE NOT IN"):
		return c.cancel(args)
	case strings.Contains(normalized, "ATTEMPTS = 0"):
		return c.requeue(args)
	case strings.Contains(normalized, "LAST_ERROR = $2"):
		return c.pause(args)
	case strings.Contains(normalized, "AVAILABLE_AT = NOW()"):
		return c.release(args)
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
	c.state.record(query, args)
	normalized := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(normalized, "UPDATE") && strings.Contains(normalized, "SKIP LOCKED"):
		return c.lease(args)
	case strings.HasPrefix(normalized, "UPDATE") && strings.Contains(normalized, "RETURNING"):
		return c.renew(args)
	case strings.HasPrefix(normalized, "SELECT"):
		return c.load(args)
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

func (c *testConn) insert(args []driver.NamedValue) (driver.Result, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	// $11 carries the relative delay in seconds; the server anchors it at
	// its own NOW(), mirroring NOW() + make_interval(secs => $11).
	now := c.state.dbNow()
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
		AvailableAt:    now.Add(secondsToDuration(args[10].Value.(float64))),
		LeaseWorkerID:  stringValue(args[11].Value),
		LeaseExpiresAt: timeValue(args[12].Value),
	}
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
	ttl := secondsToDuration(args[2].Value.(float64))
	queued := asyncpkg.JobState(args[3].Value.(string))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	now := c.state.dbNow()
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
		job.LeaseExpiresAt = now.Add(ttl)
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

func (c *testConn) renew(args []driver.NamedValue) (driver.Rows, error) {
	ttl := secondsToDuration(args[0].Value.(float64))
	jobID := args[1].Value.(string)
	workerID := args[3].Value.(string)
	attempt := int(args[4].Value.(int64))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != asyncpkg.JobRunning || job.LeaseWorkerID != workerID || job.Attempts != attempt {
		return rows(nil), nil
	}
	now := c.state.dbNow()
	job.LeaseExpiresAt = now.Add(ttl)
	job.UpdatedAt = now
	c.state.rows[jobID] = job
	return rows([][]driver.Value{jobValues(job)}), nil
}

func (c *testConn) complete(args []driver.NamedValue) (driver.Result, error) {
	state := asyncpkg.JobState(args[0].Value.(string))
	jobID := args[1].Value.(string)
	workerID := args[3].Value.(string)
	attempt := int(args[4].Value.(int64))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != asyncpkg.JobRunning || job.LeaseWorkerID != workerID || job.Attempts != attempt {
		return driver.RowsAffected(0), nil
	}
	job.State = state
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = c.state.dbNow()
	c.state.rows[jobID] = job
	return driver.RowsAffected(1), nil
}

func (c *testConn) pause(args []driver.NamedValue) (driver.Result, error) {
	state := asyncpkg.JobState(args[0].Value.(string))
	result := stringValue(args[1].Value)
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
	job.LastError = result
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = c.state.dbNow()
	c.state.rows[jobID] = job
	return driver.RowsAffected(1), nil
}

func (c *testConn) release(args []driver.NamedValue) (driver.Result, error) {
	state := asyncpkg.JobState(args[0].Value.(string))
	jobID := args[1].Value.(string)
	workerID := args[3].Value.(string)
	attempt := int(args[4].Value.(int64))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != asyncpkg.JobRunning || job.LeaseWorkerID != workerID || job.Attempts != attempt {
		return driver.RowsAffected(0), nil
	}
	now := c.state.dbNow()
	job.State = state
	job.AvailableAt = now
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = now
	c.state.rows[jobID] = job
	return driver.RowsAffected(1), nil
}

func (c *testConn) requeue(args []driver.NamedValue) (driver.Result, error) {
	state := asyncpkg.JobState(args[0].Value.(string))
	jobID := args[1].Value.(string)
	dead := asyncpkg.JobState(args[2].Value.(string))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != dead {
		return driver.RowsAffected(0), nil
	}
	now := c.state.dbNow()
	job.State = state
	job.Attempts = 0
	job.LastError = ""
	job.AvailableAt = now
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
	backoff := secondsToDuration(args[3].Value.(float64))
	jobID := args[4].Value.(string)
	workerID := args[6].Value.(string)
	attempt := int(args[7].Value.(int64))
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State != asyncpkg.JobRunning || job.LeaseWorkerID != workerID || job.Attempts != attempt {
		return driver.RowsAffected(0), nil
	}
	now := c.state.dbNow()
	if job.Attempts >= job.MaxAttempts {
		job.State = dead
	} else {
		job.State = queued
		job.AvailableAt = now.Add(backoff)
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
	jobID := args[1].Value.(string)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	job, ok := c.state.rows[jobID]
	if !ok || job.State == asyncpkg.JobCompleted || job.State == asyncpkg.JobDeadLetter {
		return driver.RowsAffected(0), nil
	}
	job.State = state
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = c.state.dbNow()
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
