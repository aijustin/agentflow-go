package runtime

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
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
	ErrRunInProgress         = errors.New("runtime: run is already running")
	ErrRunPaused           = errors.New("runtime: run is paused")
	ErrRunFailed           = errors.New("runtime: run has failed")
	ErrRunCancelled        = errors.New("runtime: run is cancelled")
)

func (e *Engine) maxAttempts(agent core.Agent) int {
	retries := firstPositive(agent.Policy.RetryLimit, e.scenario.Runtime.MaxRetries)
	return retries + 1
}

func (e *Engine) ResolveAgentName(name string) (string, error) {
	agent, err := e.resolveAgent(name)
	if err != nil {
		return "", err
	}
	return agent.Name, nil
}

func (e *Engine) llmProfile(name string) (core.LLMProfileRef, error) {
	if name == "" {
		if e.llm == nil {
			return core.LLMProfileRef{}, nil
		}
		return core.LLMProfileRef{}, fmt.Errorf("runtime: agent llm profile is required")
	}
	profile, ok := e.scenario.LLMs[name]
	if !ok {
		if e.llm == nil {
			return core.LLMProfileRef{}, nil
		}
		return core.LLMProfileRef{}, fmt.Errorf("runtime: llm profile %q not found in scenario", name)
	}
	return profile, nil
}

func (e *Engine) ensureRunActive(ctx context.Context, runID string) error {
	if runID == "" || e.runs == nil {
		return nil
	}
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return err
	}
	switch loaded.Status {
	case runstate.RunStatusRunning:
		return nil
	case runstate.RunStatusCompleted:
		return ErrRunAlreadyCompleted
	case runstate.RunStatusCancelled:
		return ErrRunCancelled
	default:
		return fmt.Errorf("runtime: run %q is not running (status=%s)", runID, loaded.Status)
	}
}

func (e *Engine) ensureRunPaused(ctx context.Context, runID string) error {
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return nil
	}
	snapshot.Status = runstate.RunStatusPaused
	return e.runs.Save(ctx, &snapshot, snapshot.Version)
}

func (e *Engine) resolveAgent(name string) (core.Agent, error) {
	agentName := name
	if agentName == "" {
		names := make([]string, 0, len(e.scenario.Agents))
		for candidate := range e.scenario.Agents {
			names = append(names, candidate)
		}
		if len(names) == 0 {
			return core.Agent{}, fmt.Errorf("runtime: no agents configured")
		}
		slices.Sort(names)
		agentName = names[0]
	}
	agent, ok := e.scenario.Agents[agentName]
	if !ok {
		return core.Agent{}, fmt.Errorf("runtime: agent %q not found", agentName)
	}
	return agent, nil
}

// beginRun creates a new run snapshot or resumes an existing one for
// continued execution. It reports only an error: no caller has ever used
// the "did this resume an existing run" signal it once also returned, so
// that dead bool was removed rather than kept in an unused state.
func (e *Engine) beginRun(ctx context.Context, req *RunRequest) error {
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	existing, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err == nil {
		wasCompleted := existing.Status == runstate.RunStatusCompleted
		switch existing.Status {
		case runstate.RunStatusCompleted:
			if _, hasFinal := existing.StepOutputs["final"]; hasFinal {
				return ErrRunAlreadyCompleted
			}
		case runstate.RunStatusCancelled:
			return ErrRunCancelled
		case runstate.RunStatusPaused:
			return ErrRunPaused
		case runstate.RunStatusFailed:
			return ErrRunFailed
		case runstate.RunStatusRunning:
			if autonomousRunInProgress(existing) {
				return ErrRunInProgress
			}
		}
		saveCtx := ctx
		if wasCompleted {
			saveCtx = runstate.ContextWithStatusTransitionOverride(ctx)
		}
		return e.saveSnapshotWithRetry(saveCtx, req.RunID, func(snapshot *runstate.RunSnapshot) error {
			snapshot.Status = runstate.RunStatusRunning
			saveResumeMetadata(snapshot, *req)
			return nil
		})
	}
	if !errors.Is(err, runstate.ErrNotFound) {
		return err
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: e.scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input":         req.Context,
			runStartedAtVar: json.RawMessage(fmt.Sprintf("%q", time.Now().UTC().Format(time.RFC3339Nano))),
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	saveResumeMetadata(&snapshot, *req)
	runstate.StampTenant(ctx, &snapshot)
	if err := e.runs.Save(ctx, &snapshot, 0); err != nil {
		if errors.Is(err, runstate.ErrStaleSnapshot) {
			// Another caller created this run first between our
			// not-found load and this save; the conflict is a race, not
			// evidence the run already completed. Re-dispatch through the
			// normal existing-run path so whatever status the winner left
			// behind (Running, Completed, Paused, ...) is classified
			// correctly instead of always reporting ErrRunAlreadyCompleted.
			return e.beginRun(ctx, req)
		}
		return err
	}
	e.emit(ctx, core.EventRunStarted, req.RunID, nil)
	return nil
}

// autonomousRunInProgress reports whether a Running snapshot looks like an
// in-flight autonomous run (as opposed to a workflow-prepared snapshot waiting
// for RunHybrid/RunStructured to continue). Two concurrent Run() calls against
// the same run ID would both see an empty-step Running snapshot; rejecting
// that case prevents duplicate execution without blocking hybrid continuation
// where the workflow phase has already written step outputs.
func autonomousRunInProgress(snapshot runstate.RunSnapshot) bool {
	if len(snapshot.StepOutputs) == 0 {
		return true
	}
	if _, hasFinal := snapshot.StepOutputs["final"]; hasFinal {
		return false
	}
	for key := range snapshot.StepOutputs {
		if strings.HasPrefix(key, "tool.") || strings.HasPrefix(key, "agent.") {
			return true
		}
	}
	return false
}

// markRunFailedOrCancelled classifies err and persists the run as Cancelled
// when it stems from the caller's context being explicitly cancelled, or as
// Failed otherwise (a deadline timeout is still a genuine failure). This
// mirrors the classification Stream already applies to its tool-loop
// goroutine, so a caller-initiated cancellation is never recorded - and
// counted in metrics - as a run failure.
func (e *Engine) markRunFailedOrCancelled(ctx context.Context, runID string, err error) {
	if errors.Is(err, context.Canceled) {
		e.markRunCancelled(ctx, runID)
		return
	}
	e.markRunFailed(ctx, runID, err)
}

func (e *Engine) completeStreamRun(ctx context.Context, runID string, agent core.Agent, prompt string, output string) error {
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return err
	}
	if loaded.Status != runstate.RunStatusRunning {
		// The run was paused, cancelled, or failed by a concurrent writer
		// while this stream was still producing output; do not clobber
		// that terminal/paused state with Completed.
		return fmt.Errorf("runtime: cannot complete run %q in status %s", runID, loaded.Status)
	}
	// Tool loops persist user/assistant/tool messages incrementally inside
	// answerWithTools; only plain chat streams need a final memory write
	// here. Write memory before persisting the terminal Completed status so
	// a memory failure never leaves the run marked complete with missing
	// history.
	if len(agent.Tools) == 0 && len(agent.SubAgents) == 0 {
		if err := e.writeMemory(ctx, runID, agent, []memoryMessage{
			{Role: string(llm.RoleUser), Content: prompt},
			{Role: string(llm.RoleAssistant), Content: output},
		}); err != nil {
			return err
		}
	}
	loaded, err = runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return err
	}
	if loaded.Status != runstate.RunStatusRunning {
		return fmt.Errorf("runtime: cannot complete run %q in status %s", runID, loaded.Status)
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
	e.recordRunCompleted(ctx, loaded)
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
		return fmt.Errorf("runtime: runstate repository is required to save step output")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Retry on stale snapshot so concurrent writers to the same run (tool
	// outputs, plan updates) do not lose this step output via optimistic
	// concurrency conflicts, matching the orchestration saveStepOutput.
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
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
	return false
}

const (
	runStartedAtVar    = "run_started_at"
	runErrorMessageVar = "run_error_message"
	resumePromptVar    = "resume_prompt"
	resumeAgentVar     = "resume_agent"
)

func saveResumeMetadata(snapshot *runstate.RunSnapshot, req RunRequest) {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	if req.Prompt != "" {
		snapshot.Variables[resumePromptVar] = json.RawMessage(fmt.Sprintf("%q", req.Prompt))
	}
	if req.Agent != "" {
		snapshot.Variables[resumeAgentVar] = json.RawMessage(fmt.Sprintf("%q", req.Agent))
	}
}

func (e *Engine) saveSnapshotWithRetry(ctx context.Context, runID string, mutate func(*runstate.RunSnapshot) error) error {
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
		if err != nil {
			return err
		}
		if mutate != nil {
			if err := mutate(&snapshot); err != nil {
				return err
			}
		}
		if err := e.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("runtime: failed to save snapshot %q after stale retries", runID)
}

// pauseWithRetry pauses through the human gate, retrying the whole
// load-then-pause sequence when a concurrent writer advances the run version
// between our load and the gate's own compare-and-swap save. Without this
// retry, a HumanGate.Pause implementation that uses a single fixed expected
// version turns a legitimate concurrent write into a hard run failure
// instead of a pause.
func (e *Engine) pauseWithRetry(ctx context.Context, runID string, build func(version int64) core.CheckpointState) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
		if err != nil {
			return "", err
		}
		token, err := e.gate.Pause(ctx, build(snapshot.Version))
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return "", err
		}
	}
	return "", fmt.Errorf("runtime: failed to pause run %q after stale snapshot retries", runID)
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

// runDuration reports how long a run has been executing, preferring the
// run_started_at variable stamped by beginRun (most precise: it survives
// resumes and always reflects the true first Running transition), then
// falling back to the snapshot's CreatedAt - which every runstate
// repository implementation stamps via StampSnapshot regardless of which
// code path created the run, including workflow/hybrid snapshots created
// directly by the framework layer rather than through beginRun. Returns 0
// if neither is available so callers can skip the histogram observation
// rather than record a meaningless zero-based duration.
func runDuration(snapshot runstate.RunSnapshot) time.Duration {
	if startedAt := variableString(snapshot.Variables, runStartedAtVar); startedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
			return time.Since(parsed)
		}
	}
	if !snapshot.CreatedAt.IsZero() {
		return time.Since(snapshot.CreatedAt)
	}
	return 0
}

// recordRunCompleted records the duration histogram and completed-event
// counter for a finished run. Only Run() used to record these; every other
// completion path (RunStructured, RunHybrid, completeRun, completeStreamRun)
// silently skipped them, leaving gaps in dashboards built on these metrics.
func (e *Engine) recordRunCompleted(ctx context.Context, snapshot runstate.RunSnapshot) {
	if d := runDuration(snapshot); d > 0 {
		e.recorder.ObserveHistogram(ctx, observability.MetricRunDurationSeconds, d.Seconds(),
			observability.Attribute{Key: "scenario", Value: e.scenario.Name})
	}
	e.recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal,
		observability.Attribute{Key: "event", Value: string(core.EventRunCompleted)},
		observability.Attribute{Key: "scenario", Value: e.scenario.Name})
}

// nonRunningCompletionResult builds the RunResult/error pair to return when
// a completion path discovers the run is no longer Running: some other
// concurrent writer already moved it to Paused/Cancelled/Failed between the
// answer producing its output and this reload. Reporting Paused/Cancelled/
// Failed as a structured RunResult - the same way this engine already
// reports a pause discovered synchronously within a single call - lets
// callers branch on RunResult.Status instead of receiving an opaque
// "cannot complete" error that hides which terminal state the run actually
// ended up in.
func nonRunningCompletionResult(runID string, status runstate.RunStatus) (RunResult, error) {
	switch status {
	case runstate.RunStatusPaused, runstate.RunStatusCancelled, runstate.RunStatusFailed:
		return RunResult{RunID: runID, Status: status}, nil
	default:
		return RunResult{}, fmt.Errorf("runtime: cannot complete run %q in status %s", runID, status)
	}
}

func (e *Engine) markRunFailed(ctx context.Context, runID string, cause error) {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	if snapshot, err := runstate.LoadAuthorized(persistCtx, e.runs, runID); err == nil {
		if snapshot.Status == runstate.RunStatusCancelled {
			e.emit(persistCtx, core.EventRunCancelled, runID, nil)
			return
		}
		if snapshot.Status != runstate.RunStatusFailed && !snapshot.Status.CanTransitionTo(runstate.RunStatusFailed) {
			e.logWarn(persistCtx, "runtime: refusing invalid failure status transition", "run_id", runID, "status", snapshot.Status)
			return
		}
		snapshot.Status = runstate.RunStatusFailed
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		// Persist the failure reason on the snapshot itself, not just in
		// the emitted event: reloading a failed run later (e.g. from a
		// separate diagnostic tool, or after the event bus has rotated old
		// events out) would otherwise give no indication of why it failed.
		snapshot.Variables[runErrorMessageVar] = json.RawMessage(fmt.Sprintf("%q", cause.Error()))
		if saveErr := e.runs.Save(persistCtx, &snapshot, snapshot.Version); saveErr != nil {
			e.logWarn(persistCtx, "runtime: failed to persist run failure status", "run_id", runID, "save_error", saveErr)
			return
		}
	}
	e.emit(persistCtx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q}`, cause.Error())))
}

func (e *Engine) markRunCancelled(ctx context.Context, runID string) {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	if snapshot, err := runstate.LoadAuthorized(persistCtx, e.runs, runID); err == nil {
		if snapshot.Status != runstate.RunStatusCancelled {
			if !snapshot.Status.CanTransitionTo(runstate.RunStatusCancelled) {
				e.logWarn(persistCtx, "runtime: refusing invalid cancellation status transition", "run_id", runID, "status", snapshot.Status)
				return
			}
			snapshot.Status = runstate.RunStatusCancelled
			if saveErr := e.runs.Save(persistCtx, &snapshot, snapshot.Version); saveErr != nil {
				e.logWarn(persistCtx, "runtime: failed to persist run cancellation status", "run_id", runID, "save_error", saveErr)
				return
			}
		}
	}
	e.emit(persistCtx, core.EventRunCancelled, runID, nil)
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
