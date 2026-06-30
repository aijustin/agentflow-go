package runtime

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

var (
	ErrRunAlreadyCompleted = errors.New("runtime: run already completed")
	ErrRunCancelled        = errors.New("runtime: run is cancelled")
)

func (e *Engine) maxAttempts(agent core.Agent) int {
	retries := firstPositive(agent.Policy.RetryLimit, e.scenario.Runtime.MaxRetries)
	return retries + 1
}

func (e *Engine) resolveAgent(name string) (core.Agent, error) {
	agentName := name
	if agentName == "" {
		for candidate := range e.scenario.Agents {
			agentName = candidate
			break
		}
	}
	agent, ok := e.scenario.Agents[agentName]
	if !ok {
		return core.Agent{}, fmt.Errorf("runtime: agent %q not found", agentName)
	}
	return agent, nil
}

// beginRun creates a new run snapshot or resumes an existing one for continued execution.
func (e *Engine) beginRun(ctx context.Context, req *RunRequest) (continued bool, err error) {
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	existing, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err == nil {
		switch existing.Status {
		case runstate.RunStatusCompleted:
			if _, hasFinal := existing.StepOutputs["final"]; hasFinal {
				return false, ErrRunAlreadyCompleted
			}
		case runstate.RunStatusCancelled:
			return false, ErrRunCancelled
		}
		existing.Status = runstate.RunStatusRunning
		if err := e.runs.Save(ctx, &existing, existing.Version); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, runstate.ErrNotFound) {
		return false, err
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: e.scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input": req.Context,
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	runstate.StampTenant(ctx, &snapshot)
	if err := e.runs.Save(ctx, &snapshot, 0); err != nil {
		return false, err
	}
	e.emit(ctx, core.EventRunStarted, req.RunID, nil)
	return false, nil
}

func (e *Engine) completeStreamRun(ctx context.Context, runID string, agent core.Agent, prompt string, output string) error {
	loaded, err := e.runs.Load(ctx, runID)
	if err != nil {
		return err
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRaw := []byte(fmt.Sprintf(`{"text":%q}`, output))
	finalRef, err := e.stepOutputRef(ctx, runID, "final", finalRaw)
	if err != nil {
		return err
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return err
	}
	// Tool loops persist user/assistant/tool messages incrementally inside
	// answerWithTools; only plain chat streams need a final memory write here.
	if len(agent.Tools) == 0 && len(agent.SubAgents) == 0 {
		if err := e.writeMemory(ctx, runID, agent, []memoryMessage{
			{Role: string(llm.RoleUser), Content: prompt},
			{Role: string(llm.RoleAssistant), Content: output},
		}); err != nil {
			return err
		}
	}
	e.emit(ctx, core.EventRunCompleted, runID, loaded.StepOutputs["final"].Inline)
	return nil
}

func (e *Engine) delegateTarget(agent core.Agent, toolName string) (string, bool) {
	for _, name := range agent.SubAgents {
		if delegateToolName(name) == toolName {
			if _, ok := e.scenario.Agents[name]; ok {
				return name, true
			}
		}
	}
	return "", false
}

func delegateToolName(agentName string) string {
	var b strings.Builder
	b.WriteString("delegate_")
	for _, r := range agentName {
		switch {
		case r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (e *Engine) saveStepOutput(ctx context.Context, runID, key string, value any) error {
	if e.runs == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Retry on stale snapshot so concurrent writers to the same run (tool
	// outputs, plan updates) do not lose this step output via optimistic
	// concurrency conflicts, matching the orchestration saveStepOutput.
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := e.runs.Load(ctx, runID)
		if err != nil {
			return err
		}
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		ref, err := e.stepOutputRef(ctx, runID, key, raw)
		if err != nil {
			return err
		}
		snapshot.StepOutputs[key] = ref
		err = e.runs.Save(ctx, &snapshot, snapshot.Version)
		if err == nil {
			return nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return err
		}
	}
	return fmt.Errorf("runtime: failed to save step %q output after stale snapshot retries", key)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (e *Engine) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func shouldRetry(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var classified interface{ Retryable() bool }
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	return true
}

func retryDelay(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond
	for range max(0, attempt-1) {
		delay *= 2
		if delay >= 2*time.Second {
			delay = 2 * time.Second
			break
		}
	}
	// Add ±25% jitter to prevent thundering-herd on concurrent retries.
	jitter := time.Duration(rand.N(int64(delay/2))) - delay/4
	delay += jitter
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (e *Engine) markRunFailed(ctx context.Context, runID string, cause error) {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	if snapshot, err := e.runs.Load(persistCtx, runID); err == nil {
		snapshot.Status = runstate.RunStatusFailed
		if saveErr := e.runs.Save(persistCtx, &snapshot, snapshot.Version); saveErr != nil {
			e.logWarn(persistCtx, "runtime: failed to persist run failure status", "run_id", runID, "save_error", saveErr)
		}
	}
	e.emit(persistCtx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q}`, cause.Error())))
}

func (e *Engine) stepOutputRef(ctx context.Context, runID, key string, raw json.RawMessage) (runstate.StepOutputRef, error) {
	if e.redactor != nil {
		redacted, err := e.redactor.RedactOutput(ctx, governance.OutputRedaction{RunID: runID, StepID: key, Kind: "step_output", Data: raw})
		if err != nil {
			return runstate.StepOutputRef{}, err
		}
		raw = redacted
	}
	threshold := e.scenario.Runtime.StepOutputThreshold
	if threshold <= 0 || int64(len(raw)) <= threshold || e.blobs == nil {
		return runstate.StepOutputRef{Inline: raw}, nil
	}
	ref, err := e.blobs.Put(ctx, raw)
	if err != nil {
		return runstate.StepOutputRef{}, err
	}
	return runstate.StepOutputRef{Blob: &ref}, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func mustMarshal(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"error":"marshal failed"}`)
	}
	return raw
}

func (e *Engine) shouldPauseBeforeFinal() bool {
	if !e.scenario.Orchestration.HumanInLoop.Enabled {
		return false
	}
	for _, checkpoint := range e.scenario.Orchestration.HumanInLoop.Checkpoints {
		if checkpoint == "before_final_answer" {
			return true
		}
	}
	return false
}

func (e *Engine) emit(ctx context.Context, typ core.EventType, runID string, payload json.RawMessage) {
	payload = governance.RedactEventPayload(ctx, e.redactor, runID, typ, payload)
	event := core.Event{
		Type:         typ,
		RunID:        runID,
		ScenarioName: e.scenario.Name,
		Timestamp:    time.Now().UTC(),
		Payload:      payload,
	}
	if traceID, spanID := observability.TraceFromContext(ctx); traceID != "" {
		event.TraceID = traceID
		event.SpanID = spanID
	}
	if parentSpanID := observability.ParentSpanFromContext(ctx); parentSpanID != "" {
		event.ParentSpanID = parentSpanID
	}
	_ = e.events.Emit(ctx, event)
}

// logWarn logs a warning message if a Logger is configured; otherwise it is
// silently discarded.
func (e *Engine) logWarn(ctx context.Context, msg string, keysAndValues ...any) {
	if e.logger != nil {
		e.logger.Warn(ctx, msg, keysAndValues...)
	}
}

func (e *Engine) emitJSON(ctx context.Context, typ core.EventType, runID string, payload any) {
	payload = enrichEventPayload(ctx, payload)
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	e.emit(ctx, typ, runID, raw)
}

func enrichEventPayload(ctx context.Context, payload any) any {
	nodeID := core.WorkflowNodeFromContext(ctx)
	if nodeID == "" {
		return payload
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	if _, exists := m["node_id"]; !exists {
		m["node_id"] = nodeID
	}
	return m
}

func (e *Engine) startSpan(ctx context.Context, name observability.SpanName, attrs ...observability.Attribute) (context.Context, observability.Span) {
	return e.tracer.Start(ctx, name, attrs...)
}

// generateRunID returns a cryptographically random run identifier with a
// "run-" prefix.  Falls back to a nanosecond timestamp on the rare occasion
// that the random reader fails.
func generateRunID() string {
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
