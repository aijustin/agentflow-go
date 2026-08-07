package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// isEmptyLLMTurn reports a conversation-contract violation: the provider
// returned neither answer content nor tool calls, and the turn was not cut
// short by the output-token budget (finish_reason "length" has its own
// dedicated error path).
func isEmptyLLMTurn(resp llm.ToolCallResponse) bool {
	return len(resp.ToolCalls) == 0 &&
		strings.TrimSpace(resp.Message.Content) == "" &&
		resp.FinishReason != "length"
}

// toolArgsRepairDiagnostic reports whether NormalizeToolArguments had to
// repair raw by collapsing malformed/truncated arguments to {}, and returns a
// diagnostic carrying the original parse error. Empty input and the
// string-encoded-arguments convention unwrap cleanly and are not repairs.
func toolArgsRepairDiagnostic(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false
	}
	candidate := trimmed
	var encoded string
	if err := json.Unmarshal(trimmed, &encoded); err == nil {
		if strings.TrimSpace(encoded) == "" {
			return "", false
		}
		candidate = json.RawMessage(encoded)
	}
	if json.Valid(candidate) {
		return "", false
	}
	var value any
	parseErr := json.Unmarshal(candidate, &value)
	return fmt.Sprintf("tool arguments were not valid JSON (%v) and were replaced with {}", parseErr), true
}

// toolArgsRepairSet records per-tool-call argument repair diagnostics for one
// run, keyed by the stable tool call ID, so a later ValidateInput failure can
// tell the model its arguments were rewritten before validation.
type toolArgsRepairSet struct {
	mu     sync.Mutex
	byCall map[string]string
}

func (e *Engine) recordToolArgsRepair(runID, callID, diagnostic string) {
	if callID == "" {
		return
	}
	set, _ := e.coord.toolArgsRepairs.LoadOrStore(runID, &toolArgsRepairSet{byCall: map[string]string{}})
	repairs := set.(*toolArgsRepairSet)
	repairs.mu.Lock()
	defer repairs.mu.Unlock()
	repairs.byCall[callID] = diagnostic
}

func (e *Engine) toolArgsRepairFor(runID, callID string) string {
	set, ok := e.coord.toolArgsRepairs.Load(runID)
	if !ok {
		return ""
	}
	repairs := set.(*toolArgsRepairSet)
	repairs.mu.Lock()
	defer repairs.mu.Unlock()
	return repairs.byCall[callID]
}
