package stdio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/mcp"
)

// Cancelling a call permanently poisons the client, so the server it was
// talking to can never serve another request. Leaving it running orphaned a
// process per cancelled call and left the reader goroutine blocked on a pipe
// that nothing would ever close.
func TestCancelledCallTerminatesServerProcess(t *testing.T) {
	client, err := NewClient(context.Background(), Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"AGENTFLOW_TEST_MCP_STDIO=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	pid := client.cmd.Process.Pid
	if pid <= 0 {
		t.Fatal("expected a started server process")
	}

	// The "hang" method never gets a reply, so the call ends on its own
	// deadline with the response still in flight.
	hangCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.call(hangCtx, "hang", nil, nil); err == nil {
		t.Fatal("expected the cancelled call to fail")
	}

	// Checked before Close, which would tear the process down anyway and hide
	// the leak. The kill is asynchronous and nothing has reaped the child yet,
	// so it is either gone or a zombie awaiting Wait.
	if !processStopped(t, pid) {
		t.Fatalf("expected the server process %d to be killed by the cancelled call, it is still running", pid)
	}
}

// The client stays poisoned after a cancelled call, so a later call fails fast
// rather than reading a stale line as its own response.
func TestCancelledCallLeavesClientPoisoned(t *testing.T) {
	client, err := NewClient(context.Background(), Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"AGENTFLOW_TEST_MCP_STDIO=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	hangCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.call(hangCtx, "hang", nil, nil); err == nil {
		t.Fatal("expected the cancelled call to fail")
	}

	followUp, followCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer followCancel()
	_, err = client.CallTool(followUp, mcp.CallToolRequest{Name: "search"})
	if err == nil {
		t.Fatal("expected the poisoned client to reject a later call")
	}
}

// processStopped reports whether pid has stopped running. An unreaped child
// lingers as a zombie until Wait collects it, so "stopped" means gone or in
// state Z, not merely absent from the process table.
func processStopped(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return true
		}
		// The state letter follows the parenthesized comm field, which may
		// itself contain spaces.
		if index := bytes.LastIndexByte(stat, ')'); index >= 0 {
			fields := strings.Fields(string(stat[index+1:]))
			if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
