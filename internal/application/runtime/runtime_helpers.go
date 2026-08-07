package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/aijustin/agentflow-go/internal/application/emit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/retry"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

var (
	ErrRunAlreadyCompleted = errors.New("runtime: run already completed")
	ErrRunInProgress       = errors.New("runtime: run is already running")
	ErrRunPaused           = errors.New("runtime: run is paused")
	ErrRunFailed           = errors.New("runtime: run has failed")
	ErrRunCancelled        = errors.New("runtime: run is cancelled")
	// ErrLLMGatewayRequired reports that a run needs an LLM call but no
	// gateway is wired. It is a permanent configuration error: blind retries
	// can never succeed, so checkpoint-continue paths classify it as
	// permanent (run marked Failed) instead of keeping the run Running for a
	// transient retry.
	ErrLLMGatewayRequired = errors.New("runtime: llm gateway is required")
	// ErrMaxStepsExceeded marks exhaustion of the autonomous tool loop step
	// budget (including replan attempts). It is a sentinel so terminal
	// handling can attribute the failure (termination_reason
	// max_steps_exceeded) instead of re-parsing the error text.
	ErrMaxStepsExceeded = errors.New("runtime: autonomous tool loop exceeded max_steps")
	// ErrTokenBudgetExceeded marks exhaustion of the run's accumulated token
	// budget (scenario runtime max_total_tokens). Sentinel so terminal
	// handling attributes the failure as termination_reason budget_exceeded.
	ErrTokenBudgetExceeded = errors.New("runtime: run exceeded max_total_tokens budget")
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
	if runID == "" || e.persist.runs == nil {
		return nil
	}
	loaded, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
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
		// Defaulting is only unambiguous with a single agent. Silently
		// picking the alphabetically first of several routed requests to
		// whichever agent happened to sort first.
		if len(names) > 1 {
			slices.Sort(names)
			return core.Agent{}, fmt.Errorf("runtime: multiple agents configured (%s); specify the agent explicitly", strings.Join(names, ", "))
		}
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
	existing, err := runstate.LoadAuthorized(ctx, e.persist.runs, req.RunID)
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
			stampLeaseOwner(ctx, snapshot)
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
			runStartedAtVar: jsonStringValue(time.Now().UTC().Format(time.RFC3339Nano)),
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	saveResumeMetadata(&snapshot, *req)
	stampLeaseOwner(ctx, &snapshot)
	runstate.StampTenant(ctx, &snapshot)
	if err := e.saveRunSnapshot(ctx, &snapshot, 0); err != nil {
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
	e.emitJSON(ctx, core.EventRunStarted, req.RunID, runStartedPayload(*req))
	return nil
}

func runStartedPayload(req RunRequest) map[string]any {
	payload := map[string]any{}
	if req.Agent != "" {
		payload["agent"] = req.Agent
	}
	if req.TrustMode != "" {
		payload["trust_mode"] = string(req.TrustMode)
	}
	for key, value := range core.FrameworkBuildFields() {
		payload[key] = value
	}
	return payload
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
	return retry.Retryable(ctx, err)
}

const (
	runStartedAtVar      = "run_started_at"
	runErrorMessageVar   = runstate.VarRunErrorMessage
	resumePromptVar      = runstate.VarResumePrompt
	resumeAgentVar       = runstate.VarResumeAgent
	resumeTrustModeVar   = runstate.VarResumeTrustMode
	resumeEpisodeIDVar   = runstate.VarResumeEpisodeID
	resumeTriggerKindVar = runstate.VarResumeTriggerKind
	resumeSessionIDVar   = runstate.VarResumeSessionID
)

func saveResumeMetadata(snapshot *runstate.RunSnapshot, req RunRequest) {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	if req.Prompt != "" {
		snapshot.Variables[resumePromptVar] = jsonStringValue(req.Prompt)
	}
	if req.Agent != "" {
		snapshot.Variables[resumeAgentVar] = jsonStringValue(req.Agent)
	}
	if req.TrustMode != "" {
		snapshot.Variables[resumeTrustModeVar] = jsonStringValue(string(req.TrustMode))
	}
	if req.EpisodeID != "" {
		snapshot.Variables[resumeEpisodeIDVar] = jsonStringValue(req.EpisodeID)
	}
	if req.TriggerKind != "" {
		snapshot.Variables[resumeTriggerKindVar] = jsonStringValue(req.TriggerKind)
	}
	if req.SessionID != "" {
		snapshot.Variables[resumeSessionIDVar] = jsonStringValue(req.SessionID)
	}
}

func episodeCorrelationFromRequest(req RunRequest) core.EpisodeCorrelation {
	return core.EpisodeCorrelation{
		EpisodeID:   req.EpisodeID,
		TriggerKind: req.TriggerKind,
		SessionID:   req.SessionID,
	}
}

func episodeCorrelationFromSnapshot(snapshot runstate.RunSnapshot) core.EpisodeCorrelation {
	return core.EpisodeCorrelation{
		EpisodeID:   variableString(snapshot.Variables, resumeEpisodeIDVar),
		TriggerKind: variableString(snapshot.Variables, resumeTriggerKindVar),
		SessionID:   variableString(snapshot.Variables, resumeSessionIDVar),
	}
}

func (e *Engine) withEpisodeCorrelation(ctx context.Context, req RunRequest) context.Context {
	corr := episodeCorrelationFromRequest(req)
	if corr.Empty() && req.RunID != "" {
		if snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, req.RunID); err == nil {
			corr = episodeCorrelationFromSnapshot(snapshot)
		}
	}
	return core.ContextWithEpisodeCorrelation(ctx, corr)
}

func ContextWithTrustMode(ctx context.Context, mode TrustMode) context.Context {
	return core.ContextWithTrustMode(ctx, string(mode))
}

func TrustModeFromContext(ctx context.Context) TrustMode {
	return TrustMode(core.TrustModeFromContext(ctx))
}

type runLeaseOwnerKey struct{}

// ContextWithRunLeaseOwner stamps the identity of the worker holding the
// run's distributed lease onto the context, so run snapshots created while
// the lease is held record ownership. MarkAbandonedRuns only reaps Running
// runs carrying this marker; runs executed without lease coordination never
// look like zombies.
func ContextWithRunLeaseOwner(ctx context.Context, owner string) context.Context {
	if owner == "" {
		return ctx
	}
	return context.WithValue(ctx, runLeaseOwnerKey{}, owner)
}

// RunLeaseOwnerFromContext returns the lease owner attached by
// ContextWithRunLeaseOwner, or "".
func RunLeaseOwnerFromContext(ctx context.Context) string {
	owner, _ := ctx.Value(runLeaseOwnerKey{}).(string)
	return owner
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata))
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

// jsonStringValue encodes s as a JSON string value. It replaces the former
// fmt.Sprintf("%q", s) sites: %q produces Go string literals whose \xNN
// escapes are invalid JSON, silently corrupting any snapshot variable or
// event payload built from a string containing control characters.
func jsonStringValue(s string) json.RawMessage {
	return mustMarshal(s)
}

func (e *Engine) hasBeforeFinalCheckpoint(agent core.Agent) bool {
	hitl := e.scenario.Orchestration.HumanInLoop
	if hitl.Enabled && core.HasHumanCheckpoint(hitl.Checkpoints, core.CheckpointBeforeFinalAnswer) {
		return true
	}
	return core.HasHumanCheckpoint(agent.Policy.HumanCheckpoints, core.CheckpointBeforeFinalAnswer)
}

func (e *Engine) emit(ctx context.Context, typ core.EventType, runID string, payload json.RawMessage) {
	e.obs.emitter.Emit(ctx, e.scenario.Name, e.gov.redactor, typ, runID, payload)
}

// IsCriticalLifecycleEvent reports whether typ is a run-lifecycle event whose
// silent loss would corrupt downstream state tracking. Delivery of these
// events is synchronous and retried with backoff before being given up.
// Deprecated alias for emit.IsCriticalLifecycleEvent.
func IsCriticalLifecycleEvent(typ core.EventType) bool {
	return emit.IsCriticalLifecycleEvent(typ)
}

// EmitWithLifecycleRetry delivers one event via sink with lifecycle retry
// semantics. Deprecated alias for emit.EmitWithLifecycleRetry.
func EmitWithLifecycleRetry(ctx context.Context, sink core.EventSink, event core.Event) error {
	return emit.EmitWithLifecycleRetry(ctx, sink, event)
}

// logWarn logs a warning message if a Logger is configured; otherwise it is
// silently discarded.
func (e *Engine) logWarn(ctx context.Context, msg string, keysAndValues ...any) {
	if e.obs.logger != nil {
		e.obs.logger.Warn(ctx, msg, keysAndValues...)
	}
}

// logError logs an error message if a Logger is configured; otherwise it is
// silently discarded.
func (e *Engine) logError(ctx context.Context, msg string, keysAndValues ...any) {
	if e.obs.logger != nil {
		e.obs.logger.Error(ctx, msg, keysAndValues...)
	}
}

func (e *Engine) emitJSON(ctx context.Context, typ core.EventType, runID string, payload any) {
	payload = enrichEventPayload(ctx, payload)
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = mustMarshal(map[string]string{"error": err.Error()})
	}
	e.emit(ctx, typ, runID, raw)
}

// emitSkillApplied notifies observers which skills shaped the agent's
// instructions for this turn. Prompt fragments are merged at scenario build
// time; this event is the runtime signal that those skills are in effect.
func (e *Engine) emitSkillApplied(ctx context.Context, runID string, agent core.Agent) {
	if len(agent.Skills) == 0 {
		return
	}
	for _, skillName := range agent.Skills {
		skill, ok := e.scenario.Skills[skillName]
		kind := skill.Kind
		if kind == "" {
			kind = core.SkillKindPrompt
		}
		payload := map[string]any{
			"agent": agent.Name,
			"skill": skillName,
			"kind":  kind,
		}
		if ok && skill.Description != "" {
			payload["description"] = skill.Description
		}
		e.emitJSON(ctx, core.EventSkillApplied, runID, payload)
	}
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
	return e.obs.tracer.Start(ctx, name, attrs...)
}

// generateRunID returns a cryptographically random run identifier with a
// "run-" prefix. The canonical implementation lives in runstate so the
// framework facade, engine, event router, and async adapter share one
// 128-bit generator instead of carrying private 64-bit copies.
func generateRunID() string {
	return runstate.GenerateRunID()
}
