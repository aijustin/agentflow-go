package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRepositorySaveStampsTimestamps(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	repo, err := NewRepository(Config{Addr: server.addr, KeyPrefix: "agentflow:test:"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot.CreatedAt.IsZero() || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps on save, got created=%v updated=%v", snapshot.CreatedAt, snapshot.UpdatedAt)
	}
	loaded, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CreatedAt.IsZero() || loaded.UpdatedAt.IsZero() {
		t.Fatalf("expected persisted timestamps, got %+v", loaded)
	}
}

func TestRepositorySavesLoadsAndDeletesSnapshots(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	repo, err := NewRepository(Config{Addr: server.addr, KeyPrefix: "agentflow:test:"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("version = %d, want 1", snapshot.Version)
	}
	loaded, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != "run-1" || loaded.Version != 1 || loaded.Status != runstate.RunStatusRunning {
		t.Fatalf("unexpected loaded snapshot: %+v", loaded)
	}
	if err := repo.Delete(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Load(ctx, "run-1"); !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

// TestRepositoryReusesPooledConnections proves that sequential operations
// reuse a warm connection from the idle pool instead of dialing (and
// re-authenticating) on every call.
func TestRepositoryReusesPooledConnections(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	repo, err := NewRepository(Config{Addr: server.addr, KeyPrefix: "agentflow:test:"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := repo.Load(ctx, "run-1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := server.acceptedConns(); got != 1 {
		t.Fatalf("expected a single pooled connection, server accepted %d", got)
	}
}

// TestRepositoryRecoversFromClosedPooledConnection verifies that a pooled
// connection closed by the server is detected and replaced rather than
// surfacing a spurious error to the caller.
func TestRepositoryRecoversFromClosedPooledConnection(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	repo, err := NewRepository(Config{Addr: server.addr, KeyPrefix: "agentflow:test:"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	// Forcibly close the idle connection to simulate a server-side idle
	// timeout, then confirm the next op transparently dials a fresh one.
	repo.mu.Lock()
	idle := repo.idle
	repo.idle = nil
	repo.mu.Unlock()
	for _, c := range idle {
		c.close()
	}
	if _, err := repo.Load(ctx, "run-1"); err != nil {
		t.Fatalf("expected recovery from closed pooled connection, got %v", err)
	}
}

func TestRepositoryRejectsStaleSnapshots(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	repo, err := NewRepository(Config{Addr: server.addr})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	stale := snapshot
	stale.Status = runstate.RunStatusPaused
	if err := repo.Save(ctx, &stale, 0); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected stale snapshot error, got %v", err)
	}
	snapshot.Status = runstate.RunStatusPaused
	if err := repo.Save(ctx, &snapshot, 1); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("version = %d, want 2", snapshot.Version)
	}
}

type fakeRedis struct {
	listener net.Listener
	addr     string
	mu       sync.Mutex
	values   map[string]map[string]string
	accepted int
}

func (server *fakeRedis) acceptedConns() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.accepted
}

func newFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeRedis{listener: listener, addr: listener.Addr().String(), values: make(map[string]map[string]string)}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (server *fakeRedis) serve() {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		server.mu.Lock()
		server.accepted++
		server.mu.Unlock()
		go server.handle(conn)
	}
}

func (server *fakeRedis) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		args, err := readCommand(reader)
		if err != nil {
			return
		}
		server.reply(conn, args)
	}
}

func (server *fakeRedis) reply(writer io.Writer, args []string) {
	if len(args) == 0 {
		_, _ = io.WriteString(writer, "-ERR empty command\r\n")
		return
	}
	switch strings.ToUpper(args[0]) {
	case "AUTH", "SELECT":
		_, _ = io.WriteString(writer, "+OK\r\n")
	case "EVAL":
		server.replyEval(writer, args)
	case "HGET":
		server.replyHGet(writer, args)
	case "DEL":
		server.replyDel(writer, args)
	case "SCAN":
		server.replyScan(writer, args)
	default:
		_, _ = io.WriteString(writer, "-ERR unsupported command\r\n")
	}
}

func (server *fakeRedis) replyEval(writer io.Writer, args []string) {
	key := args[3]
	expected := args[4]
	next := args[5]
	snapshot := args[6]
	server.mu.Lock()
	defer server.mu.Unlock()
	fields, exists := server.values[key]
	if expected == "0" {
		if exists {
			_, _ = io.WriteString(writer, ":0\r\n")
			return
		}
		server.values[key] = map[string]string{"version": next, "snapshot": snapshot}
		_, _ = io.WriteString(writer, ":1\r\n")
		return
	}
	if !exists || fields["version"] != expected {
		_, _ = io.WriteString(writer, ":0\r\n")
		return
	}
	fields["version"] = next
	fields["snapshot"] = snapshot
	_, _ = io.WriteString(writer, ":1\r\n")
}

func (server *fakeRedis) replyHGet(writer io.Writer, args []string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	fields, exists := server.values[args[1]]
	if !exists {
		_, _ = io.WriteString(writer, "$-1\r\n")
		return
	}
	value, exists := fields[args[2]]
	if !exists {
		_, _ = io.WriteString(writer, "$-1\r\n")
		return
	}
	_, _ = fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(value), value)
}

func (server *fakeRedis) replyDel(writer io.Writer, args []string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	_, exists := server.values[args[1]]
	delete(server.values, args[1])
	if exists {
		_, _ = io.WriteString(writer, ":1\r\n")
		return
	}
	_, _ = io.WriteString(writer, ":0\r\n")
}

func (server *fakeRedis) replyScan(writer io.Writer, args []string) {
	pattern := "*"
	for i := 1; i < len(args); i++ {
		if strings.EqualFold(args[i], "MATCH") && i+1 < len(args) {
			pattern = args[i+1]
			i++
		}
	}
	server.mu.Lock()
	keys := make([]string, 0, len(server.values))
	for key := range server.values {
		if matchRedisPattern(pattern, key) {
			keys = append(keys, key)
		}
	}
	server.mu.Unlock()
	_, _ = fmt.Fprintf(writer, "*2\r\n$1\r\n0\r\n*%d\r\n", len(keys))
	for _, key := range keys {
		_, _ = fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(key), key)
	}
}

func matchRedisPattern(pattern, key string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(key, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == key
}

func readCommand(reader *bufio.Reader) ([]string, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if prefix != '*' {
		return nil, fmt.Errorf("unexpected prefix %q", prefix)
	}
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(line)
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for range count {
		if marker, err := reader.ReadByte(); err != nil || marker != '$' {
			return nil, fmt.Errorf("expected bulk string")
		}
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		length, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		data := make([]byte, length+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		args = append(args, string(data[:length]))
	}
	return args, nil
}

func TestRepositoryListFiltersThreadAndParent(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	repo, err := NewRepository(Config{Addr: server.addr, KeyPrefix: "agentflow:test:"})
	if err != nil {
		t.Fatal(err)
	}
	parent := runstate.RunSnapshot{RunID: "run-parent", ScenarioName: "scenario", Status: runstate.RunStatusCompleted}
	child := runstate.RunSnapshot{
		RunID:        "run-child",
		ScenarioName: "scenario",
		Status:       runstate.RunStatusRunning,
		ParentRunID:  "run-parent",
		ThreadID:     "thread-1",
	}
	other := runstate.RunSnapshot{RunID: "run-other", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	for _, snap := range []runstate.RunSnapshot{parent, child, other} {
		copy := snap
		if err := repo.Save(ctx, &copy, 0); err != nil {
			t.Fatal(err)
		}
	}
	threadRuns, err := repo.List(ctx, runstate.ListFilter{ThreadID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threadRuns) != 1 || threadRuns[0].RunID != "run-child" {
		t.Fatalf("unexpected thread filter result: %+v", threadRuns)
	}
	forks, err := repo.List(ctx, runstate.ListFilter{ParentRunID: "run-parent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(forks) != 1 || forks[0].RunID != "run-child" {
		t.Fatalf("unexpected parent filter result: %+v", forks)
	}
}
