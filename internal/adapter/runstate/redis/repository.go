package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
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
}

type Repository struct {
	config Config
	prefix string
	dial   DialContextFunc
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
	return &Repository{config: config, prefix: prefix, dial: dial}, nil
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
				continue
			}
			if filter.Status != "" && snap.Status != filter.Status {
				continue
			}
			if filter.ScenarioName != "" && snap.ScenarioName != filter.ScenarioName {
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
	conn, err := r.dial(ctx, "tcp", r.config.Addr)
	if err != nil {
		return respValue{}, fmt.Errorf("redis runstate: dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	reader := bufio.NewReader(conn)
	if r.config.Password != "" {
		if _, err := r.roundTrip(conn, reader, "AUTH", r.config.Password); err != nil {
			return respValue{}, err
		}
	}
	if r.config.DB > 0 {
		if _, err := r.roundTrip(conn, reader, "SELECT", strconv.Itoa(r.config.DB)); err != nil {
			return respValue{}, err
		}
	}
	return r.roundTrip(conn, reader, args...)
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
