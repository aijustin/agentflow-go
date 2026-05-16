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
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
)

func TestLockerAcquireRenewRelease(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	locker, err := NewLocker(Config{Addr: server.addr, KeyPrefix: "agentflow:"})
	if err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := locker.Acquire(ctx, "run:1", "worker:1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected lease to be acquired")
	}
	if lease.Key != "run:1" || lease.Owner != "worker:1" || lease.ExpiresAt.IsZero() {
		t.Fatalf("unexpected lease: %+v", lease)
	}
	_, acquired, err = locker.Acquire(ctx, "run:1", "worker:2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("expected second acquire to fail")
	}
	renewed, ok, err := locker.Renew(ctx, lease, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !renewed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("expected renewed lease, got ok=%v lease=%+v", ok, renewed)
	}
	if err := locker.Release(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	_, acquired, err = locker.Acquire(ctx, "run:1", "worker:2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected acquire after release")
	}
}

func TestLockerDoesNotReleaseLeaseOwnedByAnotherWorker(t *testing.T) {
	ctx := context.Background()
	server := newFakeRedis(t)
	locker, err := NewLocker(Config{Addr: server.addr})
	if err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := locker.Acquire(ctx, "run:1", "worker:1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected lease to be acquired")
	}
	lease.Owner = "worker:2"
	if err := locker.Release(ctx, lease); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid lease, got %v", err)
	}
	_, acquired, err = locker.Acquire(ctx, "run:1", "worker:2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("wrong owner should not release the lease")
	}
}

func TestLockerValidatesInputs(t *testing.T) {
	if _, err := NewLocker(Config{}); err == nil {
		t.Fatal("expected missing address error")
	}
	if _, err := NewLocker(Config{Addr: "127.0.0.1:6379", DB: -1}); err == nil {
		t.Fatal("expected invalid db error")
	}
	server := newFakeRedis(t)
	locker, err := NewLocker(Config{Addr: server.addr})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := locker.Acquire(context.Background(), "", "owner", time.Minute); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid key error, got %v", err)
	}
	if _, _, err := locker.Acquire(context.Background(), "key", "owner", 0); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid ttl error, got %v", err)
	}
	if _, _, err := locker.Renew(context.Background(), coordination.Lease{Key: "key", Owner: "owner"}, 0); !errors.Is(err, coordination.ErrInvalidLease) {
		t.Fatalf("expected invalid renew ttl error, got %v", err)
	}
}

type fakeRedis struct {
	listener net.Listener
	addr     string
	mu       sync.Mutex
	values   map[string]string
}

func newFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeRedis{listener: listener, addr: listener.Addr().String(), values: make(map[string]string)}
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
	case "SET":
		server.replySet(writer, args)
	case "EVAL":
		server.replyEval(writer, args)
	default:
		_, _ = io.WriteString(writer, "-ERR unsupported command\r\n")
	}
}

func (server *fakeRedis) replySet(writer io.Writer, args []string) {
	key := args[1]
	owner := args[2]
	server.mu.Lock()
	defer server.mu.Unlock()
	if _, exists := server.values[key]; exists {
		_, _ = io.WriteString(writer, "$-1\r\n")
		return
	}
	server.values[key] = owner
	_, _ = io.WriteString(writer, "+OK\r\n")
}

func (server *fakeRedis) replyEval(writer io.Writer, args []string) {
	script := args[1]
	key := args[3]
	owner := args[4]
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.values[key] != owner {
		_, _ = io.WriteString(writer, ":0\r\n")
		return
	}
	if strings.Contains(script, "PEXPIRE") {
		_, _ = io.WriteString(writer, ":1\r\n")
		return
	}
	delete(server.values, key)
	_, _ = io.WriteString(writer, ":1\r\n")
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
