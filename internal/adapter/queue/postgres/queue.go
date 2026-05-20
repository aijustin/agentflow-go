package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

const DefaultTableName = "agentflow_jobs"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Option func(*Queue) error

type Queue struct {
	db    *sql.DB
	table string
	now   func() time.Time
}

func NewQueue(db *sql.DB, opts ...Option) (*Queue, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres queue: db is nil")
	}
	queue := &Queue{db: db, table: DefaultTableName, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(queue); err != nil {
			return nil, err
		}
	}
	return queue, nil
}

func WithTableName(name string) Option {
	return func(queue *Queue) error {
		if !validTableName(name) {
			return fmt.Errorf("postgres queue: invalid table name %q", name)
		}
		queue.table = name
		return nil
	}
}

func (q *Queue) Enqueue(ctx context.Context, job asyncpkg.Job) (asyncpkg.Job, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Job{}, err
	}
	if err := job.Validate(); err != nil {
		return asyncpkg.Job{}, err
	}
	now := q.now().UTC()
	if job.State == "" {
		job.State = asyncpkg.JobQueued
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 1
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = now
	}
	job.Attempts = 0
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	query := fmt.Sprintf(`INSERT INTO %s (id, type, run_id, payload_json, state, attempts, max_attempts, last_error, created_at, updated_at, available_at, lease_worker_id, lease_expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO NOTHING`, q.table)
	result, err := q.db.ExecContext(ctx, query, job.ID, job.Type, nullString(job.RunID), []byte(job.Payload), string(job.State), int64(job.Attempts), int64(job.MaxAttempts), nullString(job.LastError), job.CreatedAt, job.UpdatedAt, job.AvailableAt, nullString(job.LeaseWorkerID), nullTime(job.LeaseExpiresAt))
	if err != nil {
		return asyncpkg.Job{}, fmt.Errorf("postgres queue: enqueue job %q: %w", job.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return asyncpkg.Job{}, fmt.Errorf("postgres queue: enqueue job %q: %w", job.ID, err)
	}
	if affected != 1 {
		return asyncpkg.Job{}, fmt.Errorf("postgres queue: job %q already exists: %w", job.ID, asyncpkg.ErrInvalidJob)
	}
	return asyncpkg.CloneJob(job), nil
}

func (q *Queue) Lease(ctx context.Context, workerID string, ttl time.Duration) (asyncpkg.Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Lease{}, false, err
	}
	if workerID == "" || ttl <= 0 {
		return asyncpkg.Lease{}, false, asyncpkg.ErrStaleLease
	}
	now := q.now().UTC()
	expires := now.Add(ttl)
	query := fmt.Sprintf(`UPDATE %s
SET state = $1, attempts = attempts + 1, lease_worker_id = $2, lease_expires_at = $3, updated_at = $4
WHERE id = (
	SELECT id FROM %s
	WHERE (state = $5 AND available_at <= $4) OR (state = $6 AND lease_expires_at < $4)
	ORDER BY created_at, id
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
RETURNING id, type, run_id, payload_json, state, attempts, max_attempts, last_error, created_at, updated_at, available_at, lease_worker_id, lease_expires_at`, q.table, q.table)
	job, err := q.scanJob(q.db.QueryRowContext(ctx, query, string(asyncpkg.JobRunning), workerID, expires, now, string(asyncpkg.JobQueued), string(asyncpkg.JobRunning)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return asyncpkg.Lease{}, false, nil
		}
		return asyncpkg.Lease{}, false, fmt.Errorf("postgres queue: lease job: %w", err)
	}
	return asyncpkg.Lease{JobID: job.ID, WorkerID: workerID, Attempt: job.Attempts, ExpiresAt: job.LeaseExpiresAt}, true, nil
}

func (q *Queue) Load(ctx context.Context, jobID string) (asyncpkg.Job, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Job{}, err
	}
	query := fmt.Sprintf(`SELECT id, type, run_id, payload_json, state, attempts, max_attempts, last_error, created_at, updated_at, available_at, lease_worker_id, lease_expires_at
FROM %s WHERE id = $1`, q.table)
	job, err := q.scanJob(q.db.QueryRowContext(ctx, query, jobID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return asyncpkg.Job{}, asyncpkg.ErrJobNotFound
		}
		return asyncpkg.Job{}, fmt.Errorf("postgres queue: load job %q: %w", jobID, err)
	}
	return job, nil
}

func (q *Queue) Renew(ctx context.Context, lease asyncpkg.Lease, ttl time.Duration) (asyncpkg.Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Lease{}, false, err
	}
	if err := lease.Validate(); err != nil {
		return asyncpkg.Lease{}, false, err
	}
	if ttl <= 0 {
		return asyncpkg.Lease{}, false, asyncpkg.ErrStaleLease
	}
	now := q.now().UTC()
	expires := now.Add(ttl)
	query := fmt.Sprintf(`UPDATE %s
SET lease_expires_at = $1, updated_at = $2
WHERE id = $3 AND state = $4 AND lease_worker_id = $5 AND attempts = $6
RETURNING id, type, run_id, payload_json, state, attempts, max_attempts, last_error, created_at, updated_at, available_at, lease_worker_id, lease_expires_at`, q.table)
	job, err := q.scanJob(q.db.QueryRowContext(ctx, query, expires, now, lease.JobID, string(asyncpkg.JobRunning), lease.WorkerID, int64(lease.Attempt)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return asyncpkg.Lease{}, false, asyncpkg.ErrStaleLease
		}
		return asyncpkg.Lease{}, false, fmt.Errorf("postgres queue: renew job %q: %w", lease.JobID, err)
	}
	return asyncpkg.Lease{JobID: job.ID, WorkerID: job.LeaseWorkerID, Attempt: job.Attempts, ExpiresAt: job.LeaseExpiresAt}, true, nil
}

func (q *Queue) Complete(ctx context.Context, lease asyncpkg.Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	query := fmt.Sprintf(`UPDATE %s
SET state = $1, lease_worker_id = NULL, lease_expires_at = NULL, updated_at = $2
WHERE id = $3 AND state = $4 AND lease_worker_id = $5 AND attempts = $6`, q.table)
	result, err := q.db.ExecContext(ctx, query, string(asyncpkg.JobCompleted), q.now().UTC(), lease.JobID, string(asyncpkg.JobRunning), lease.WorkerID, int64(lease.Attempt))
	if err != nil {
		return fmt.Errorf("postgres queue: complete job %q: %w", lease.JobID, err)
	}
	return requireAffected(result)
}

func (q *Queue) Fail(ctx context.Context, lease asyncpkg.Lease, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	now := q.now().UTC()
	query := fmt.Sprintf(`UPDATE %s
SET state = CASE WHEN attempts >= max_attempts THEN $1 ELSE $2 END,
	last_error = $3,
	available_at = CASE WHEN attempts >= max_attempts THEN available_at ELSE $4 END,
	lease_worker_id = NULL,
	lease_expires_at = NULL,
	updated_at = $4
WHERE id = $5 AND state = $6 AND lease_worker_id = $7 AND attempts = $8`, q.table)
	result, err := q.db.ExecContext(ctx, query, string(asyncpkg.JobDeadLetter), string(asyncpkg.JobQueued), nullString(reason), now, lease.JobID, string(asyncpkg.JobRunning), lease.WorkerID, int64(lease.Attempt))
	if err != nil {
		return fmt.Errorf("postgres queue: fail job %q: %w", lease.JobID, err)
	}
	return requireAffected(result)
}

func (q *Queue) Cancel(ctx context.Context, jobID string) error {
	job, err := q.Load(ctx, jobID)
	if err != nil {
		return err
	}
	if job.State == asyncpkg.JobCompleted || job.State == asyncpkg.JobDeadLetter {
		return nil
	}
	query := fmt.Sprintf(`UPDATE %s
SET state = $1, lease_worker_id = NULL, lease_expires_at = NULL, updated_at = $2
WHERE id = $3 AND state NOT IN ($4, $5)`, q.table)
	result, err := q.db.ExecContext(ctx, query, string(asyncpkg.JobCancelled), q.now().UTC(), jobID, string(asyncpkg.JobCompleted), string(asyncpkg.JobDeadLetter))
	if err != nil {
		return fmt.Errorf("postgres queue: cancel job %q: %w", jobID, err)
	}
	return requireAffected(result)
}

func (q *Queue) scanJob(row interface{ Scan(dest ...any) error }) (asyncpkg.Job, error) {
	var job asyncpkg.Job
	var runID sql.NullString
	var payload []byte
	var state string
	var lastError sql.NullString
	var leaseWorkerID sql.NullString
	var leaseExpiresAt sql.NullTime
	var attempts int64
	var maxAttempts int64
	if err := row.Scan(&job.ID, &job.Type, &runID, &payload, &state, &attempts, &maxAttempts, &lastError, &job.CreatedAt, &job.UpdatedAt, &job.AvailableAt, &leaseWorkerID, &leaseExpiresAt); err != nil {
		return asyncpkg.Job{}, err
	}
	job.RunID = runID.String
	job.Payload = append(job.Payload[:0], payload...)
	job.State = asyncpkg.JobState(state)
	job.Attempts = int(attempts)
	job.MaxAttempts = int(maxAttempts)
	job.LastError = lastError.String
	job.LeaseWorkerID = leaseWorkerID.String
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = leaseExpiresAt.Time
	}
	return asyncpkg.CloneJob(job), nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return asyncpkg.ErrStaleLease
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func validTableName(name string) bool {
	if name == "" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !identifierPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func (q *Queue) ListJobs(ctx context.Context, filter asyncpkg.JobFilter) ([]asyncpkg.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT id, type, run_id, payload_json, state, attempts, max_attempts, last_error, created_at, updated_at, available_at, lease_worker_id, lease_expires_at FROM %s`, q.table)
	args := make([]any, 0, 2)
	if filter.State != "" {
		query += ` WHERE state = $1`
		args = append(args, string(filter.State))
	}
	query += ` ORDER BY created_at ASC`
	if filter.Limit > 0 {
		if len(args) == 0 {
			query += fmt.Sprintf(` LIMIT %d`, filter.Limit)
		} else {
			query += fmt.Sprintf(` LIMIT %d`, filter.Limit)
		}
	}
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]asyncpkg.Job, 0)
	for rows.Next() {
		job, err := q.scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (q *Queue) Requeue(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := q.now().UTC()
	query := fmt.Sprintf(`UPDATE %s SET state = $1, attempts = 0, last_error = NULL, updated_at = $2, available_at = $2, lease_worker_id = NULL, lease_expires_at = NULL WHERE id = $3 AND state = $4`, q.table)
	result, err := q.db.ExecContext(ctx, query, string(asyncpkg.JobQueued), now, jobID, string(asyncpkg.JobDeadLetter))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		job, loadErr := q.Load(ctx, jobID)
		if loadErr != nil {
			return loadErr
		}
		if job.State != asyncpkg.JobDeadLetter {
			return asyncpkg.ErrInvalidJob
		}
		return asyncpkg.ErrStaleLease
	}
	return nil
}
