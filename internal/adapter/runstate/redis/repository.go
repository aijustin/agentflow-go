package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const defaultKeyPrefix = "agentflow:runstate:"

const saveScript = `
local version = redis.call("HGET", KEYS[1], "version")
if ARGV[1] == "0" then
  if version then
    return 0
  end
else
  if not version or version ~= ARGV[1] then
    return 0
  end
end
redis.call("HSET", KEYS[1], "version", ARGV[2], "snapshot", ARGV[3])
return 1
`

type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

type Config struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	DialContext  DialContextFunc
	// MaxIdleConns bounds the number of authenticated connections kept warm
	// for reuse. Defaults to defaultMaxIdleConns. Set to a negative value to
	// disable pooling (dial a fresh connection per operation).
	MaxIdleConns int
}

const defaultMaxIdleConns = 8

// pooledConn is an authenticated connection (AUTH/SELECT already applied) kept
// warm for reuse so callers avoid a dial + handshake on every operation.
type pooledConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (c *pooledConn) close() { _ = c.conn.Close() }

type Repository struct {
	config  Config
	prefix  string
	dial    DialContextFunc
	maxIdle int

	mu     sync.Mutex
	idle   []*pooledConn
	closed bool
}

func NewRepository(config Config) (*Repository, error) {
	if strings.TrimSpace(config.Addr) == "" {
		return nil, fmt.Errorf("redis runstate: address is required")
	}
	if config.DB < 0 {
		return nil, fmt.Errorf("redis runstate: db must be >= 0")
	}
	dial := config.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: firstDuration(config.DialTimeout, 5*time.Second)}
		dial = dialer.DialContext
	}
	prefix := config.KeyPrefix
	if prefix == "" {
		prefix = defaultKeyPrefix
	}
	maxIdle := config.MaxIdleConns
	if maxIdle == 0 {
		maxIdle = defaultMaxIdleConns
	}
	if maxIdle < 0 {
		maxIdle = 0
	}
	return &Repository{config: config, prefix: prefix, dial: dial, maxIdle: maxIdle}, nil
}

// Close releases all idle pooled connections. After Close the repository keeps
// working by dialing fresh connections; it exists so callers can promptly free
// sockets during shutdown.
func (r *Repository) Close() error {
	r.mu.Lock()
	conns := r.idle
	r.idle = nil
	r.closed = true
	r.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
	return nil
}

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if snapshot == nil {
		return runstate.ErrNotFound
	}
	if expectedVersion < 0 {
		return fmt.Errorf("redis runstate: expected version must be >= 0")
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	var previous *runstate.RunSnapshot
	if expectedVersion > 0 {
		prev, loadErr := r.Load(ctx, snapshot.RunID)
		if loadErr != nil {
			return loadErr
		}
		previous = &prev
	}
	runstate.StampSnapshot(snapshot, previous, time.Now().UTC())
	next := snapshot.Version
	if next <= expectedVersion {
		next = expectedVersion + 1
	}
	copy := *snapshot
	copy.Version = next
	payload, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("redis runstate: marshal snapshot: %w", err)
	}
	value, err := r.do(ctx, "EVAL", saveScript, "1", r.key(snapshot.RunID), strconv.FormatInt(expectedVersion, 10), strconv.FormatInt(next, 10), string(payload))
	if err != nil {
		return err
	}
	if value.integer != 1 {
		return runstate.ErrStaleSnapshot
	}
	snapshot.Version = next
	return nil
}

func (r *Repository) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	if runID == "" {
		return runstate.RunSnapshot{}, fmt.Errorf("runstate: run_id is required")
	}
	value, err := r.do(ctx, "HGET", r.key(runID), "snapshot")
	if err != nil {
		return runstate.RunSnapshot{}, err
	}
	if value.nil {
		return runstate.RunSnapshot{}, runstate.ErrNotFound
	}
	var snapshot runstate.RunSnapshot
	if err := json.Unmarshal([]byte(value.bulk), &snapshot); err != nil {
		return runstate.RunSnapshot{}, fmt.Errorf("redis runstate: unmarshal snapshot: %w", err)
	}
	return snapshot, nil
}

func (r *Repository) Delete(ctx context.Context, runID string) error {
	if runID == "" {
		return fmt.Errorf("runstate: run_id is required")
	}
	_, err := r.do(ctx, "DEL", r.key(runID))
	return err
}

// List scans all keys with the repository prefix and returns snapshots that
// satisfy the filter. Because the Redis adapter uses a raw RESP protocol
// client without connection pooling, each scan cursor iteration opens a new
// connection. For large keyspaces prefer a dedicated store with direct
// indexing (e.g. postgres).
func (r *Repository) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pattern := r.prefix + "*"
	cursor := "0"
	var out []runstate.RunSnapshot
	for {
		value, err := r.do(ctx, "SCAN", cursor, "MATCH", pattern, "COUNT", "100")
		if err != nil {
			return nil, err
		}
		if len(value.array) != 2 {
			return nil, fmt.Errorf("redis runstate: unexpected SCAN response")
		}
		cursor = value.array[0].bulk
		keys := value.array[1].array
		for _, keyVal := range keys {
			snap, err := r.Load(ctx, strings.TrimPrefix(keyVal.bulk, r.prefix))
			if err != nil {
				if errors.Is(err, runstate.ErrNotFound) {
					// Key disappeared between SCAN and GET (deleted concurrently); skip.
					continue
				}
				return out, fmt.Errorf("redis runstate: list: load %q: %w", keyVal.bulk, err)
			}
			if filter.Status != "" && snap.Status != filter.Status {
				continue
			}
			if filter.ScenarioName != "" && snap.ScenarioName != filter.ScenarioName {
				continue
			}
			if filter.TenantID != "" && snap.TenantID != filter.TenantID {
				continue
			}
			if filter.ParentRunID != "" && snap.ParentRunID != filter.ParentRunID {
				continue
			}
			if filter.ThreadID != "" && runstate.ResolveThreadID(snap) != filter.ThreadID {
				continue
			}
			out = append(out, snap)
			if filter.Limit > 0 && len(out) >= filter.Limit {
				return out, nil
			}
		}
		if cursor == "0" {
			break
		}
	}
	return out, nil
}

func (r *Repository) key(runID string) string {
	return r.prefix + runID
}

func (r *Repository) do(ctx context.Context, args ...string) (respValue, error) {
	if err := ctx.Err(); err != nil {
		return respValue{}, err
	}
	conn, err := r.acquire(ctx)
	if err != nil {
		return respValue{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.conn.SetDeadline(deadline)
	}
	value, err := r.roundTrip(conn.conn, conn.reader, args...)
	if err != nil {
		// The command result is uncertain on error, so the connection is
		// discarded rather than reused. We never retry here: the CAS EVAL is
		// not idempotent and a reused connection is liveness-checked before
		// use, so a retry could double-apply a write.
		conn.close()
		return value, err
	}
	r.release(conn)
	return value, nil
}

// acquire returns a healthy authenticated connection, reusing a warm idle one
// when available and otherwise dialing and handshaking a fresh connection.
func (r *Repository) acquire(ctx context.Context) (*pooledConn, error) {
	for {
		r.mu.Lock()
		n := len(r.idle)
		if n == 0 {
			r.mu.Unlock()
			break
		}
		conn := r.idle[n-1]
		r.idle = r.idle[:n-1]
		r.mu.Unlock()
		if connHealthy(conn.conn) {
			return conn, nil
		}
		conn.close()
	}
	return r.newConn(ctx)
}

// newConn dials a connection and performs the AUTH/SELECT handshake once, so
// pooled reuse does not repeat the handshake on every operation.
func (r *Repository) newConn(ctx context.Context) (*pooledConn, error) {
	conn, err := r.dial(ctx, "tcp", r.config.Addr)
	if err != nil {
		return nil, fmt.Errorf("redis runstate: dial: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	pc := &pooledConn{conn: conn, reader: bufio.NewReader(conn)}
	if r.config.Password != "" {
		if _, err := r.roundTrip(pc.conn, pc.reader, "AUTH", r.config.Password); err != nil {
			pc.close()
			return nil, err
		}
	}
	if r.config.DB > 0 {
		if _, err := r.roundTrip(pc.conn, pc.reader, "SELECT", strconv.Itoa(r.config.DB)); err != nil {
			pc.close()
			return nil, err
		}
	}
	return pc, nil
}

// release returns a connection to the idle pool, clearing any per-operation
// deadline first, or closes it when the pool is full or already closed.
func (r *Repository) release(conn *pooledConn) {
	_ = conn.conn.SetDeadline(time.Time{})
	r.mu.Lock()
	if r.closed || len(r.idle) >= r.maxIdle {
		r.mu.Unlock()
		conn.close()
		return
	}
	r.idle = append(r.idle, conn)
	r.mu.Unlock()
}

// connHealthy reports whether an idle connection is still usable. It performs a
// brief non-blocking read: a timeout means the peer is alive with no pending
// data (healthy), while EOF/error or unexpected bytes mean the connection is
// dead or out of sync and must be discarded.
func connHealthy(conn net.Conn) bool {
	if err := conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
		return false
	}
	var b [1]byte
	_, err := conn.Read(b[:])
	_ = conn.SetReadDeadline(time.Time{})
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

func (r *Repository) roundTrip(conn net.Conn, reader *bufio.Reader, args ...string) (respValue, error) {
	if r.config.WriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(r.config.WriteTimeout))
	}
	if err := writeCommand(conn, args...); err != nil {
		return respValue{}, fmt.Errorf("redis runstate: write command: %w", err)
	}
	if r.config.ReadTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(r.config.ReadTimeout))
	}
	value, err := readValue(reader)
	if err != nil {
		return respValue{}, err
	}
	if value.err != "" {
		return respValue{}, fmt.Errorf("redis runstate: %s", value.err)
	}
	return value, nil
}

type respValue struct {
	bulk    string
	integer int64
	nil     bool
	err     string
	array   []respValue
}

func writeCommand(writer io.Writer, args ...string) error {
	if _, err := fmt.Fprintf(writer, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return err
		}
	}
	return nil
}

func readValue(reader *bufio.Reader) (respValue, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return respValue{}, err
	}
	switch prefix {
	case '+':
		line, err := readLine(reader)
		return respValue{bulk: line}, err
	case '-':
		line, err := readLine(reader)
		return respValue{err: line}, err
	case ':':
		line, err := readLine(reader)
		if err != nil {
			return respValue{}, err
		}
		value, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return respValue{}, err
		}
		return respValue{integer: value}, nil
	case '$':
		line, err := readLine(reader)
		if err != nil {
			return respValue{}, err
		}
		length, err := strconv.Atoi(line)
		if err != nil {
			return respValue{}, err
		}
		if length == -1 {
			return respValue{nil: true}, nil
		}
		data := make([]byte, length+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return respValue{}, err
		}
		return respValue{bulk: string(data[:length])}, nil
	case '*':
		line, err := readLine(reader)
		if err != nil {
			return respValue{}, err
		}
		count, err := strconv.Atoi(line)
		if err != nil {
			return respValue{}, err
		}
		if count == -1 {
			return respValue{nil: true}, nil
		}
		elements := make([]respValue, count)
		for i := range elements {
			elem, err := readValue(reader)
			if err != nil {
				return respValue{}, err
			}
			elements[i] = elem
		}
		return respValue{array: elements}, nil
	default:
		return respValue{}, fmt.Errorf("redis runstate: unsupported response prefix %q", prefix)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func firstDuration(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
