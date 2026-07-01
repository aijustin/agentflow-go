package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

type Config struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
	Stderr  io.Writer
}

type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
	nextID  atomic.Int64
	// broken records why the client was poisoned after a call was
	// abandoned while its response was still in flight (see call below).
	// Requests are strictly sequential over one pipe with no multiplexing,
	// so once a read is abandoned mid-flight there is no safe way to tell
	// which later call an eventually-arriving line belongs to.
	broken error
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewClient(ctx context.Context, config Config) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return nil, fmt.Errorf("mcp stdio: command is required")
	}
	cmd := exec.CommandContext(ctx, command, config.Args...)
	cmd.Env = append(os.Environ(), config.Env...)
	cmd.Dir = config.Dir
	stderr := config.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp stdio: start command: %w", err)
	}
	return &Client{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout)}, nil
}

func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	var decoded struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", nil, &decoded); err != nil {
		return nil, err
	}
	return decoded.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, req mcp.CallToolRequest) (mcp.CallToolResult, error) {
	if strings.TrimSpace(req.Name) == "" {
		return mcp.CallToolResult{}, fmt.Errorf("mcp stdio: tool name is required")
	}
	params, err := json.Marshal(req)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	var result mcp.CallToolResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return mcp.CallToolResult{}, err
	}
	return result, nil
}

func (c *Client) Close() error {
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return <-done
	}
}

func (c *Client) call(ctx context.Context, method string, params json.RawMessage, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken != nil {
		return c.broken
	}
	id := c.nextID.Add(1)
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("mcp stdio: write request: %w", err)
	}

	decoded, err := c.readResponse(ctx, id)
	if err != nil {
		return err
	}
	if decoded.Error != nil {
		return fmt.Errorf("mcp stdio: rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if out == nil || len(decoded.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return fmt.Errorf("mcp stdio: decode result: %w", err)
	}
	return nil
}

// readResponse blocks on the underlying scanner in a background goroutine
// so it can be abandoned once ctx is done instead of hanging forever on a
// server that never replies. Because the pipe carries one response per
// request with no id-based demultiplexing wait loop, abandoning a read
// leaves an unread line that could belong to this request arrive at an
// arbitrary later point; if that happened it could be silently misread as
// the response to a future, unrelated call. To avoid that, the client is
// permanently poisoned once a read is abandoned this way.
func (c *Client) readResponse(ctx context.Context, id int64) (rpcResponse, error) {
	type scanResult struct {
		line []byte
		err  error
		ok   bool
	}
	resultCh := make(chan scanResult, 1)
	go func() {
		ok := c.scanner.Scan()
		var line []byte
		if ok {
			line = append([]byte(nil), c.scanner.Bytes()...)
		}
		resultCh <- scanResult{line: line, err: c.scanner.Err(), ok: ok}
	}()

	var res scanResult
	select {
	case res = <-resultCh:
	case <-ctx.Done():
		c.broken = fmt.Errorf("mcp stdio: client unusable after a call was cancelled while awaiting a response: %w", ctx.Err())
		return rpcResponse{}, ctx.Err()
	}

	if !res.ok {
		if res.err != nil {
			return rpcResponse{}, fmt.Errorf("mcp stdio: read response: %w", res.err)
		}
		return rpcResponse{}, fmt.Errorf("mcp stdio: server closed stdout")
	}
	var decoded rpcResponse
	if err := json.Unmarshal(res.line, &decoded); err != nil {
		return rpcResponse{}, fmt.Errorf("mcp stdio: decode response: %w", err)
	}
	if decoded.ID != id {
		return rpcResponse{}, fmt.Errorf("mcp stdio: response id %d does not match request id %d", decoded.ID, id)
	}
	return decoded, nil
}
