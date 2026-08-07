package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aijustin/agentflow-go/internal/application/emit"
	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/feature"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
	"github.com/aijustin/agentflow-go/pkg/toolinspect"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// Engine is decomposed into cohesive dependency groups instead of a flat
// field list: persist (run-state storage), tooling (tool dispatch and
// approval execution), gov (governance/security/audit), mem (context and
// memory), obs (observability and event delivery), coord (run coordination
// and per-run trackers), hooks (host hooks and wrappers).
type Engine struct {
	scenario core.Scenario
	llm      llm.Gateway

	persist persistDeps
	tooling toolDeps
	gov     govDeps
	mem     memoryDeps
	obs     obsDeps
	coord   coordDeps
	hooks   hookDeps
}

// persistDeps groups the run-state persistence ports.
type persistDeps struct {
	runs  runstate.Repository
	blobs runstate.BlobStore
}

// toolDeps groups tool dispatch, approval execution and catalog ports.
type toolDeps struct {
	tools                ToolRegistry
	orchestrator         toolorch.ToolOrchestrator
	approvalStore        toolorch.ApprovalStore
	denyBreaker          *toolorch.DenyBreaker
	toolCatalog          toolcatalog.Catalog
	deferredTools        bool
	toolInspectorPrepend []toolinspect.Inspector
	toolInspectorAppend  []toolinspect.Inspector
}

// govDeps groups governance, security and audit ports.
type govDeps struct {
	policy            security.Policy
	toolGov           governance.ToolPolicy
	redactor          governance.OutputRedactor
	approvalEvaluator core.ToolApprovalEvaluator
	audit             audit.Sink
}

// memoryDeps groups context/memory ports and context-shaping config.
type memoryDeps struct {
	memory                 map[string]memory.Repository
	tierMemory             map[string]tier.Manager
	cognitive              map[string]memory.CognitiveMemory
	enqueueMemoryReconcile func(context.Context, async.Job) error
	interjections          *interjection.Buffer
	interjectDrain         atomic.Value // interjection.DrainPolicy
	toolTransformMu        sync.RWMutex
	toolTransforms         map[string]contextwindow.ToolOutputTransform
	dualVisibility         bool
}

// obsDeps groups observability and event delivery ports.
type obsDeps struct {
	// emitter is the shared event pipeline (async queue + lifecycle sync
	// delivery). ownsEmitter reports whether Close must stop it; engines
	// handed a host-owned pipeline (the framework facade's) never close it.
	emitter     *emit.Pipeline
	ownsEmitter bool

	recorder          observability.Recorder
	tracer            observability.Tracer
	logger            log.Logger
	llmPayloadCapture bool
}

// coordDeps groups run-coordination ports and per-run mutable trackers.
type coordDeps struct {
	gate core.HumanGate
	// statusNotifier broadcasts in-process run-status transition hints (see
	// runStatusNotifier); the detached-stream cancellation watcher subscribes
	// so a same-process settle wakes it immediately instead of waiting for
	// the next poll tick.
	statusNotifier *runStatusNotifier

	loadedTools        sync.Map // runID -> *loadedToolSet
	pendingSelfCompact sync.Map // runID -> struct{}
	usageTrackers      sync.Map // runID -> *usageTracker
	// iterationBases tracks the conversation length persisted at the last
	// autonomous iteration boundary, so the next boundary writes only the
	// delta (see persistAutonomousIteration).
	iterationBases  sync.Map // runID -> int
	toolArgsRepairs sync.Map // runID -> *toolArgsRepairSet
}

// hookDeps groups host-provided hooks and gateway wrappers.
type hookDeps struct {
	turnStopHook          core.TurnStopHook
	loopHooks             []feature.LoopHooks
	stopConditions        []feature.StopCondition
	llmToolCallerWrappers []func(llm.ToolCaller) llm.ToolCaller
}

// Logger is the runtime logging port. Prefer pkg/log.Logger in new code.
type Logger = log.Logger

type ToolRegistry interface {
	ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error)
}

type Dependencies struct {
	LLM                   llm.Gateway
	Tools                 ToolRegistry
	Memory                map[string]memory.Repository
	TierMemory            map[string]tier.Manager
	Cognitive             map[string]memory.CognitiveMemory
	Runs                  runstate.Repository
	Blobs                 runstate.BlobStore
	Events                core.EventSink
	HumanGate             core.HumanGate
	ToolApprovalEvaluator core.ToolApprovalEvaluator
	Policy                security.Policy
	Audit                 audit.Sink
	ToolPolicy            governance.ToolPolicy
	OutputRedactor        governance.OutputRedactor
	// LLMPayloadCapture controls whether LLMCalled events include message and
	// prompt plaintext. Default false: payloads carry only message count,
	// per-message lengths, and a truncated content hash.
	LLMPayloadCapture bool
	// Recorder receives metric observations. If nil, metrics are discarded.
	Recorder observability.Recorder
	// Tracer receives distributed tracing spans. If nil, tracing is a no-op.
	Tracer observability.Tracer
	// Logger receives structured log messages for warning and error paths.
	// If nil, messages are silently discarded.
	Logger log.Logger
	// EnqueueMemoryReconcile enqueues async tier reconcile jobs after tier writes.
	EnqueueMemoryReconcile func(context.Context, async.Job) error
	// ToolOutputTransforms are optional per-tool reshapers applied before LLM/memory.
	ToolOutputTransforms map[string]contextwindow.ToolOutputTransform
	// InterjectDrain controls when mid-turn interjections enter the tool loop.
	InterjectDrain interjection.DrainPolicy
	// ToolOrchestrator optional approval cache / post-attempt hooks (sandbox is host-owned).
	ToolOrchestrator toolorch.ToolOrchestrator
	// ApprovalStore caches allow/deny decisions; when nil and orchestrator is a
	// StoreOrchestrator, that store is used. Otherwise a memory store is created
	// when HITLDenyLimit or orchestrator needs it.
	ApprovalStore toolorch.ApprovalStore
	// TurnStopHook optional host veto after a candidate final answer.
	TurnStopHook core.TurnStopHook
	// ToolCatalog optional deferred tool catalog for meta-tool discovery.
	ToolCatalog toolcatalog.Catalog
	// DeferredTools enables catalog deferral (default true when ToolCatalog set).
	DeferredTools bool
	// ToolInspectorPrepend / ToolInspectorAppend are host tool inspectors
	// running before / after the built-in dispatch gates (see pkg/toolinspect).
	ToolInspectorPrepend []toolinspect.Inspector
	ToolInspectorAppend  []toolinspect.Inspector
	// LLMToolCallerWrappers wrap the tool-calling gateway of the autonomous
	// tool loop, in order (first wrapper innermost). Collected from features.
	LLMToolCallerWrappers []func(llm.ToolCaller) llm.ToolCaller
	// LoopHooks fire after every completed tool-loop step (post persistence).
	LoopHooks []feature.LoopHooks
	// StopConditions may halt the run after a tool-executing step.
	StopConditions []feature.StopCondition
	// DualVisibilityMessages enables the dual-visibility projection: context
	// trimming marks messages visibility=user instead of dropping them, so
	// events/memory/checkpoints keep the full transcript while provider
	// gateways send only the model-visible projection. Default false.
	DualVisibilityMessages bool
	// EmitPipeline, when set, routes durable event delivery through this
	// host-owned pipeline (shared ordering, shared queue, host-managed
	// lifetime) instead of a per-engine one. The engine never closes a
	// pipeline it does not own.
	EmitPipeline *emit.Pipeline
	// EventQueueCapacity bounds the per-engine async event queue created when
	// EmitPipeline is nil. <= 0 selects emit.DefaultQueueCapacity.
	EventQueueCapacity int
}

func NewEngine(scenario core.Scenario, deps Dependencies) (*Engine, error) {
	if deps.Runs == nil {
		return nil, fmt.Errorf("runtime: runstate repository is required")
	}
	if deps.Events == nil {
		deps.Events = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	}
	recorder := deps.Recorder
	if recorder == nil {
		recorder = observability.NoopRecorder{}
	}
	tracer := deps.Tracer
	if tracer == nil {
		tracer = observability.NoopTracer{}
	}
	store := deps.ApprovalStore
	orch := deps.ToolOrchestrator
	if orch == nil && store != nil {
		orch = toolorch.NewStoreOrchestrator(store)
	}
	if store == nil {
		if so, ok := orch.(*toolorch.StoreOrchestrator); ok && so != nil && so.Store != nil {
			store = so.Store
		}
	}
	if store == nil && (scenario.Runtime.HITLDenyLimit > 0 || orch != nil) {
		store = toolorch.NewMemoryApprovalStore()
	}
	if orch == nil && store != nil {
		orch = toolorch.NewStoreOrchestrator(store)
	}
	var breaker *toolorch.DenyBreaker
	if scenario.Runtime.HITLDenyLimit > 0 {
		breaker = toolorch.NewDenyBreaker(scenario.Runtime.HITLDenyLimit)
	}
	emitter := deps.EmitPipeline
	ownsEmitter := false
	if emitter == nil {
		emitter = emit.NewPipeline(emit.Config{
			Sink:          deps.Events,
			Logger:        deps.Logger,
			Recorder:      recorder,
			QueueCapacity: deps.EventQueueCapacity,
		})
		ownsEmitter = true
	}
	engine := &Engine{
		scenario: scenario,
		llm:      deps.LLM,
		persist: persistDeps{
			runs:  deps.Runs,
			blobs: deps.Blobs,
		},
		tooling: toolDeps{
			tools:                deps.Tools,
			orchestrator:         orch,
			approvalStore:        store,
			denyBreaker:          breaker,
			toolCatalog:          deps.ToolCatalog,
			deferredTools:        deps.DeferredTools,
			toolInspectorPrepend: deps.ToolInspectorPrepend,
			toolInspectorAppend:  deps.ToolInspectorAppend,
		},
		gov: govDeps{
			policy:            deps.Policy,
			toolGov:           deps.ToolPolicy,
			redactor:          deps.OutputRedactor,
			approvalEvaluator: deps.ToolApprovalEvaluator,
			audit:             deps.Audit,
		},
		mem: memoryDeps{
			memory:                 deps.Memory,
			tierMemory:             deps.TierMemory,
			cognitive:              deps.Cognitive,
			enqueueMemoryReconcile: deps.EnqueueMemoryReconcile,
			interjections:          interjection.NewBuffer(),
			toolTransforms:         deps.ToolOutputTransforms,
			dualVisibility:         deps.DualVisibilityMessages,
		},
		obs: obsDeps{
			emitter:           emitter,
			ownsEmitter:       ownsEmitter,
			recorder:          recorder,
			tracer:            tracer,
			logger:            deps.Logger,
			llmPayloadCapture: deps.LLMPayloadCapture,
		},
		coord: coordDeps{
			gate:           deps.HumanGate,
			statusNotifier: newRunStatusNotifier(),
		},
		hooks: hookDeps{
			turnStopHook:          deps.TurnStopHook,
			loopHooks:             deps.LoopHooks,
			stopConditions:        deps.StopConditions,
			llmToolCallerWrappers: deps.LLMToolCallerWrappers,
		},
	}
	engine.mem.interjectDrain.Store(deps.InterjectDrain.Normalize())
	return engine, nil
}

// Close drains and stops the engine-owned event pipeline (bounded wait). It
// is a no-op for engines sharing a host-owned pipeline (Dependencies.EmitPipeline)
// — the host drains it. Close is idempotent and safe to call once runs have
// quiesced; in-flight runs are not interrupted.
func (e *Engine) Close() {
	if e == nil || !e.obs.ownsEmitter || e.obs.emitter == nil {
		return
	}
	e.obs.emitter.Close()
}

// DroppedEvents reports how many queued non-lifecycle events were dropped
// because the emission queue was full (see emit.Pipeline).
func (e *Engine) DroppedEvents() int64 {
	if e == nil || e.obs.emitter == nil {
		return 0
	}
	return e.obs.emitter.DroppedEvents()
}

// TrustMode controls run-scoped tool approval overrides.
type TrustMode string

const (
	TrustModeDefault   TrustMode = ""
	TrustModeFullTrust TrustMode = "full_trust"
)

type RunRequest struct {
	RunID     string          `json:"run_id"`
	Agent     string          `json:"agent,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	Context   json.RawMessage `json:"context,omitempty"`
	TrustMode TrustMode       `json:"trust_mode,omitempty"`
	// EpisodeID identifies a platform Episode (one QA test run) that may span
	// multiple Runs or HITL resumes. Distinct from thread_id (Fork only).
	EpisodeID string `json:"episode_id,omitempty"`
	// TriggerKind describes how the run was started (for example manual, webhook, resume).
	TriggerKind string `json:"trigger_kind,omitempty"`
	// SessionID optionally groups Episodes under a longer-lived product session.
	SessionID string `json:"session_id,omitempty"`
}

// SetToolOutputTransform registers or replaces a per-tool output transform.
// Safe for concurrent use with the tool loop (tests / late config).
func (e *Engine) SetToolOutputTransform(tool string, fn contextwindow.ToolOutputTransform) {
	if e == nil {
		return
	}
	e.mem.toolTransformMu.Lock()
	defer e.mem.toolTransformMu.Unlock()
	if e.mem.toolTransforms == nil {
		e.mem.toolTransforms = map[string]contextwindow.ToolOutputTransform{}
	}
	if fn == nil {
		delete(e.mem.toolTransforms, tool)
		return
	}
	e.mem.toolTransforms[tool] = fn
}

// Scenario returns the scenario the engine was constructed with.
func (e *Engine) Scenario() core.Scenario {
	if e == nil {
		return core.Scenario{}
	}
	return e.scenario
}

// LateConfig returns a copy of runtime-mutable engine config for engine rebuild.
func (e *Engine) LateConfig() (map[string]contextwindow.ToolOutputTransform, interjection.DrainPolicy) {
	if e == nil {
		return nil, interjection.DrainPolicy{}.Normalize()
	}
	e.mem.toolTransformMu.RLock()
	transforms := make(map[string]contextwindow.ToolOutputTransform, len(e.mem.toolTransforms))
	for k, v := range e.mem.toolTransforms {
		transforms[k] = v
	}
	e.mem.toolTransformMu.RUnlock()
	return transforms, e.drainPolicy()
}

func (e *Engine) toolTransformsCopy() map[string]contextwindow.ToolOutputTransform {
	if e == nil {
		return nil
	}
	e.mem.toolTransformMu.RLock()
	defer e.mem.toolTransformMu.RUnlock()
	if len(e.mem.toolTransforms) == 0 {
		return nil
	}
	out := make(map[string]contextwindow.ToolOutputTransform, len(e.mem.toolTransforms))
	for k, v := range e.mem.toolTransforms {
		out[k] = v
	}
	return out
}

func (e *Engine) drainPolicy() interjection.DrainPolicy {
	if e == nil {
		return interjection.DrainPolicy{}.Normalize()
	}
	if v := e.mem.interjectDrain.Load(); v != nil {
		return v.(interjection.DrainPolicy)
	}
	return interjection.DrainPolicy{}.Normalize()
}

type RunResult struct {
	RunID            string             `json:"run_id"`
	Status           runstate.RunStatus `json:"status"`
	Token            string             `json:"token,omitempty"`
	Output           string             `json:"output,omitempty"`
	StructuredOutput json.RawMessage    `json:"structured_output,omitempty"`
}

func (e *Engine) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = e.withTimeout(ctx, e.scenario.Runtime.Timeout)
		defer cancel()
	}
	ctx, runSpan := e.startSpan(ctx, observability.SpanRun,
		observability.Attribute{Key: "run_id", Value: req.RunID},
		observability.Attribute{Key: "scenario_name", Value: e.scenario.Name},
	)
	defer runSpan.End()

	ctx = ContextWithTrustMode(ctx, req.TrustMode)
	ctx = core.ContextWithEpisodeCorrelation(ctx, episodeCorrelationFromRequest(req))
	if err := e.beginRun(ctx, &req); err != nil {
		runSpan.RecordError(err)
		return RunResult{}, err
	}
	ctx = e.withEpisodeCorrelation(ctx, req)
	failRun := func(err error) (RunResult, error) {
		return e.failRun(ctx, runSpan, req.RunID, err)
	}

	agent, agentErr := e.resolveAgent(req.Agent)
	if agentErr != nil {
		return failRun(agentErr)
	}
	if len(agent.Policy.OutputSchema) > 0 {
		// Run() always calls answer(), which only ever produces plain text:
		// silently ignoring output_schema here would return free-text
		// output for an agent the caller configured to emit structured
		// JSON, with no indication anything was skipped. RunStructured is
		// the entry point that actually enforces the schema.
		return failRun(fmt.Errorf("runtime: agent %q has an output_schema configured; use RunStructured instead of Run", agent.Name))
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, req.RunID)
	if err != nil {
		return failRun(err)
	}
	if e.hasBeforeFinalCheckpoint(agent) && !e.isBeforeFinalResumed(snapshot) {
		if e.coord.gate == nil {
			return failRun(fmt.Errorf("runtime: human gate required for configured checkpoint"))
		}
		result, err := e.pauseBeforeFinalAnswer(ctx, req, agent, &snapshot, checkpointPauseOptions{})
		if err != nil {
			return failRun(err)
		}
		return result, nil
	}

	output, err := e.answerForAgent(ctx, req, agent)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		return failRun(err)
	}
	// persistRunCompleted re-checks on every save attempt that no concurrent
	// writer moved this run to a terminal or paused state (e.g. a tool-loop
	// pause that raced this call, or a cancellation) between answer()
	// returning and the completion save; such a state is never clobbered.
	return e.completeRun(ctx, req.RunID, output)
}

// failRun settles a run failure: records the error on the run span, persists
// the failed/cancelled status, and counts the terminal runtime event. Run and
// RunHybrid share it so every failure branch is visible in traces and metrics
// instead of only marking the snapshot failed.
func (e *Engine) failRun(ctx context.Context, runSpan observability.Span, runID string, err error) (RunResult, error) {
	runSpan.RecordError(err)
	eventType := core.EventRunFailed
	if errors.Is(err, context.Canceled) {
		eventType = core.EventRunCancelled
	}
	e.markRunFailedOrCancelled(ctx, runID, err)
	e.obs.recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal,
		observability.Attribute{Key: "event", Value: string(eventType)},
		observability.Attribute{Key: "scenario", Value: e.scenario.Name})
	return RunResult{}, err
}

func (e *Engine) RunStructured(ctx context.Context, req RunRequest) (RunResult, error) {
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = e.withTimeout(ctx, e.scenario.Runtime.Timeout)
		defer cancel()
	}
	ctx, runSpan := e.startSpan(ctx, observability.SpanRun,
		observability.Attribute{Key: "run_id", Value: req.RunID},
		observability.Attribute{Key: "scenario_name", Value: e.scenario.Name},
		observability.Attribute{Key: "structured", Value: "true"},
	)
	defer runSpan.End()
	ctx = ContextWithTrustMode(ctx, req.TrustMode)
	ctx = core.ContextWithEpisodeCorrelation(ctx, episodeCorrelationFromRequest(req))
	if err := e.beginRun(ctx, &req); err != nil {
		return RunResult{}, err
	}
	ctx = e.withEpisodeCorrelation(ctx, req)
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	if len(agent.Tools)+len(agent.SubAgents) > 0 {
		err := fmt.Errorf("runtime: agent %q has tools/sub-agents configured; RunStructured does not execute tool loops; use Run instead", agent.Name)
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	if e.hasBeforeFinalCheckpoint(agent) {
		if e.coord.gate == nil {
			err := fmt.Errorf("runtime: human gate required for configured checkpoint")
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, req.RunID)
		if err != nil {
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		if !e.isBeforeFinalResumed(snapshot) {
			result, err := e.pauseBeforeFinalAnswer(ctx, req, agent, &snapshot, checkpointPauseOptions{outputMode: "structured"})
			if err != nil {
				e.markRunFailed(ctx, req.RunID, err)
				return RunResult{}, err
			}
			return result, nil
		}
	}
	raw, err := e.structuredAnswer(ctx, req)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		e.markRunFailedOrCancelled(ctx, req.RunID, err)
		return RunResult{}, err
	}
	return e.completeStructuredRun(ctx, req.RunID, raw)
}

func (e *Engine) completeStructuredRun(ctx context.Context, runID string, raw json.RawMessage) (RunResult, error) {
	if _, err := e.persistRunCompleted(ctx, runID, raw); err != nil {
		var conflict completionConflictError
		if errors.As(err, &conflict) {
			return nonRunningCompletionResult(runID, conflict.status)
		}
		e.markRunFailedOrCancelled(ctx, runID, err)
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: string(raw), StructuredOutput: raw}, nil
}

// streamDetachedKey marks caller-detached streaming (see WithStreamDetached
// on the framework facade): client disconnect must not cancel the run.
type streamDetachedKey struct{}

// defaultDetachedCancellationPollInterval is how often the detached-stream
// cancellation watcher reloads the run snapshot when the scenario does not
// override it (see core.RuntimePolicy.DetachedCancellationPollInterval).
// Polling is only the fallback for cancellations persisted by OTHER
// processes: a same-process settle (cancel/fail/complete/pause through the
// engine's helpers) wakes the watcher immediately via the run-status
// notifier, so the poll cadence no longer needs to be sub-second — 2s keeps
// cross-process cancellations responsive while cutting the steady-state
// repository read pressure per detached run by ~8x.
const defaultDetachedCancellationPollInterval = 2 * time.Second

// detachedCancellationPollInterval returns the configured detached-stream
// cancellation poll interval, falling back to the default when the scenario
// leaves it zero or negative.
func (e *Engine) detachedCancellationPollInterval() time.Duration {
	if interval := e.scenario.Runtime.DetachedCancellationPollInterval; interval > 0 {
		return interval
	}
	return defaultDetachedCancellationPollInterval
}

// ContextWithStreamDetached marks the stream to keep executing to a terminal
// state in the background when the caller's context is cancelled (e.g. client
// disconnect), instead of marking the run Cancelled.
func ContextWithStreamDetached(ctx context.Context) context.Context {
	return context.WithValue(ctx, streamDetachedKey{}, true)
}

func streamDetachedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(streamDetachedKey{}).(bool)
	return v
}

// StreamDetachedFromContext reports whether ctx carries the detached
// streaming marker (see ContextWithStreamDetached). The framework facade
// needs it to exempt detached streams from caller-gone fallbacks: a detached
// run explicitly keeps executing after the caller goes away.
func StreamDetachedFromContext(ctx context.Context) bool {
	return streamDetachedFromContext(ctx)
}

// chunkError returns the structured provider error a failed chunk carries
// when present, falling back to the wire-safe string form, so retry
// classification and persisted failure reasons keep the original error type.
func chunkError(chunk llm.ChatChunk) error {
	if chunk.Err != nil {
		return chunk.Err
	}
	return errors.New(chunk.Error)
}

func (e *Engine) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return nil, err
	}
	// Reject before beginRun creates (and immediately has to fail) a run
	// snapshot for a request that can never succeed.
	if e.hasBeforeFinalCheckpoint(agent) {
		return nil, fmt.Errorf("runtime: streaming does not support before_final_answer checkpoint; use Run or RunStructured")
	}
	callerCtx := ctx
	detached := streamDetachedFromContext(ctx)
	// execCtx drives execution and terminal persistence. In detached mode it
	// is decoupled from the caller's context so a client disconnect does not
	// cancel the run; forwarding to the caller still observes callerCtx.
	execCtx := ctx
	if detached {
		execCtx = context.WithoutCancel(ctx)
	}
	var cancel context.CancelFunc
	if _, hasDeadline := execCtx.Deadline(); !hasDeadline {
		execCtx, cancel = e.withTimeout(execCtx, e.scenario.Runtime.Timeout)
	} else {
		cancel = func() {}
	}
	var cancelDetached context.CancelCauseFunc
	if detached {
		// A detached run ignores caller cancellation, but a lost run lease
		// must still abort it before another worker takes over the run.
		watchCtx, watchCancel := context.WithCancelCause(execCtx)
		cancelDetached = watchCancel
		execCtx = watchCtx
		defer func() {
			// Balances WithCancelCause for the synchronous part of Stream;
			// the goroutine below takes over cancellation afterwards.
			_ = watchCancel
		}()
		safecall.GoSafe("runtime: detached lease watcher", nil, func() {
			select {
			case <-callerCtx.Done():
				if cause := context.Cause(callerCtx); errors.Is(cause, coordination.ErrRunLeaseLost) {
					watchCancel(cause)
				}
			case <-watchCtx.Done():
			}
		})
	}
	execCtx = ContextWithTrustMode(execCtx, req.TrustMode)
	execCtx = core.ContextWithEpisodeCorrelation(execCtx, episodeCorrelationFromRequest(req))
	if err := e.beginRun(execCtx, &req); err != nil {
		cancel()
		return nil, err
	}
	execCtx = e.withEpisodeCorrelation(execCtx, req)
	if detached {
		observerCtx := execCtx
		safecall.GoSafe("runtime: detached cancellation watcher", nil, func() {
			// Fast path: a same-process settle of this run (cancel/fail/
			// complete/pause via the engine's helpers) broadcasts an
			// in-process hint that wakes the watcher immediately. Slow path:
			// the poll ticker remains the correctness fallback for
			// cancellations persisted by other processes, which never
			// produce a hint. Either way the watcher re-loads the snapshot
			// and confirms the status before acting — hints only cut
			// latency, they never decide.
			wake := make(chan struct{}, 1)
			unsubscribe := e.coord.statusNotifier.subscribe(req.RunID, wake)
			defer unsubscribe()
			ticker := time.NewTicker(e.detachedCancellationPollInterval())
			defer ticker.Stop()
			// check reports whether the watcher is done (run left Running,
			// or the load failed and the run was aborted).
			check := func() bool {
				snapshot, loadErr := runstate.LoadAuthorized(observerCtx, e.persist.runs, req.RunID)
				if loadErr != nil {
					cancelDetached(fmt.Errorf("runtime: observe detached run %q cancellation: %w", req.RunID, loadErr))
					return true
				}
				if snapshot.Status == runstate.RunStatusRunning {
					return false
				}
				if snapshot.Status == runstate.RunStatusCancelled {
					cancelDetached(ErrRunCancelled)
				}
				return true
			}
			for {
				select {
				case <-execCtx.Done():
					return
				case <-wake:
					if check() {
						return
					}
				case <-ticker.C:
					if check() {
						return
					}
				}
			}
		})
	}
	source, agent, streamCancel, err := e.streamAnswer(execCtx, req)
	if err != nil {
		e.markRunFailed(execCtx, req.RunID, err)
		cancel()
		return nil, err
	}
	out := make(chan llm.ChatChunk, 1)
	safecall.GoSafe("runtime: stream consumer", func(err error) {
		// The deferred cancel/streamCancel/close(out) above have already run.
		// Settle the run instead of leaving it Running forever: a crashed
		// consumer is a worker-side failure, so fail the run on a detached
		// persistence context (the consumer's own defers cancelled execCtx).
		e.markRunFailed(context.WithoutCancel(execCtx), req.RunID, err)
		e.logError(context.WithoutCancel(execCtx), "runtime: stream consumer crashed; run marked failed", "run_id", req.RunID, "error", err)
	}, func() {
		defer close(out)
		defer streamCancel()
		defer cancel()
		// forwarding reports whether the caller is still consuming. Once the
		// caller goes away, a detached run keeps executing (and settling the
		// run) without sending; a non-detached run applies the existing
		// cancelled/failed classification and stops.
		forwarding := true
		sentTerminal := false
		sendTerminal := func(c llm.ChatChunk) {
			if !forwarding || (sentTerminal && c.Error == "") {
				return
			}
			sentTerminal = true
			c.Done = true
			select {
			case out <- c:
			case <-callerCtx.Done():
			}
		}
		send := func(c llm.ChatChunk) bool {
			if !forwarding || sentTerminal {
				return false
			}
			if c.Done {
				sendTerminal(c)
				return true
			}
			select {
			case out <- c:
				return true
			case <-callerCtx.Done():
				return false
			}
		}
		// callerGone handles a forwarding failure. It reports whether the
		// consumer goroutine should stop entirely (non-detached) or keep
		// executing in the background (detached).
		callerGone := func() bool {
			if !detached {
				if err := callerCtx.Err(); errors.Is(err, context.DeadlineExceeded) {
					e.markRunFailed(execCtx, req.RunID, err)
				} else {
					e.markRunCancelled(execCtx, req.RunID)
				}
				return true
			}
			forwarding = false
			return false
		}
		var b strings.Builder
		toolsAgent := len(agent.Tools) > 0 || len(agent.SubAgents) > 0
		for chunk := range source {
			// Presentation answer deltas (non-terminal) feed the plain-chat
			// aggregate. Tool progress chunks must not. Done.Content is handled
			// below: tools path treats it as authoritative final; plain chat may
			// deliver content+Done in one chunk.
			if !chunk.Done && chunk.IsAnswerContent() && chunk.Content != "" {
				b.WriteString(chunk.Content)
			}
			if chunk.Done {
				finalOutput := b.String()
				if toolsAgent {
					if chunk.Content != "" {
						// answerWithTools returns terminal prose without tool-turn
						// preambles; prefer it over concatenated presentation tokens.
						finalOutput = chunk.Content
					}
					// When deltas were already streamed, strip Done.Content so
					// consumers do not receive a duplicate bulk prose frame.
					if b.Len() > 0 {
						chunk.Content = ""
					}
				} else if chunk.Content != "" && b.Len() == 0 {
					// Plain chat providers/mocks may send content only on Done.
					finalOutput = chunk.Content
				} else if b.Len() > 0 {
					// Content already forwarded via deltas; avoid re-emitting on Done.
					chunk.Content = ""
				}
				if !send(chunk) && callerGone() {
					return
				}
				if chunk.Paused {
					if err := e.ensureRunPaused(execCtx, req.RunID); err != nil {
						e.logWarn(execCtx, "runtime: failed to persist paused status after stream pause", "run_id", req.RunID, "error", err)
					}
					return
				}
				if chunk.Error != "" {
					e.markRunFailed(execCtx, req.RunID, chunkError(chunk))
					return
				}
				if err := e.completeStreamRun(execCtx, req.RunID, agent, req.Prompt, finalOutput); err != nil {
					// A completion that failed because the caller's context
					// died while the final Done chunk raced the send must
					// still settle the run; otherwise a cancelled-caller
					// stream would leave it Running forever.
					if ctxErr := execCtx.Err(); ctxErr != nil {
						e.markRunFailedOrCancelled(execCtx, req.RunID, ctxErr)
					}
					sendTerminal(llm.ChatChunk{Error: err.Error()})
				}
				return
			}
			if !send(chunk) {
				if callerGone() {
					return
				}
				continue
			}
			if chunk.Paused {
				if err := e.ensureRunPaused(execCtx, req.RunID); err != nil {
					e.logWarn(execCtx, "runtime: failed to persist paused status after stream pause", "run_id", req.RunID, "error", err)
				}
				return
			}
			if chunk.Error != "" {
				e.markRunFailed(execCtx, req.RunID, chunkError(chunk))
				return
			}
		}
		if err := execCtx.Err(); err != nil {
			if errors.Is(context.Cause(execCtx), ErrRunCancelled) {
				e.markRunCancelled(execCtx, req.RunID)
			} else if errors.Is(err, context.DeadlineExceeded) || detached {
				// Apart from explicit run cancellation, detached execution is
				// cancelled only by its own scenario timeout or a lost lease,
				// so those execution-context errors are genuine failures.
				e.markRunFailed(execCtx, req.RunID, err)
			} else {
				e.markRunCancelled(execCtx, req.RunID)
			}
			return
		}
		// The source closed without ever sending a Done chunk: the provider
		// stream was cut off mid-answer. Treating it as a normal completion
		// would persist a truncated output as the run's final answer.
		streamErr := errors.New("runtime: llm stream closed without a done chunk")
		e.markRunFailed(execCtx, req.RunID, streamErr)
		sendTerminal(llm.ChatChunk{Error: streamErr.Error()})
	})
	return out, nil
}

func (e *Engine) RunAgent(ctx context.Context, agentName string, input core.AgentInput) (core.AgentOutput, error) {
	if err := e.ensureRunActive(ctx, input.RunID); err != nil {
		return core.AgentOutput{}, err
	}
	agent, err := e.resolveAgent(agentName)
	if err != nil {
		return core.AgentOutput{}, err
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, input.RunID)
	if err != nil {
		return core.AgentOutput{}, err
	}
	if e.hasBeforeFinalCheckpoint(agent) && !e.isBeforeFinalResumed(snapshot) {
		if e.coord.gate == nil {
			return core.AgentOutput{}, fmt.Errorf("runtime: human gate required for configured checkpoint")
		}
		req := RunRequest{
			RunID:   input.RunID,
			Agent:   agentName,
			Prompt:  input.Prompt,
			Context: input.Context,
		}
		result, err := e.pauseBeforeFinalAnswer(ctx, req, agent, &snapshot, checkpointPauseOptions{})
		if err != nil {
			return core.AgentOutput{}, err
		}
		return core.AgentOutput{}, RunPausedError{RunID: result.RunID, Token: result.Token, Kind: "before_final_answer"}
	}
	// Stamp the conversation memory watermark for this workflow node before
	// it appends any turns, so workflow time-travel can rewind memory in step
	// with rewound step outputs.
	if err := e.recordConversationWatermark(ctx, input.RunID, agent); err != nil {
		return core.AgentOutput{}, err
	}
	req := RunRequest{
		RunID:   input.RunID,
		Agent:   agentName,
		Prompt:  input.Prompt,
		Context: input.Context,
	}
	if len(agent.Policy.OutputSchema) > 0 {
		raw, err := e.structuredAnswer(ctx, req)
		if err != nil {
			return core.AgentOutput{}, err
		}
		return core.AgentOutput{RunID: input.RunID, Text: string(raw), Raw: raw}, nil
	}
	output, err := e.answer(ctx, req)
	if err != nil {
		return core.AgentOutput{}, err
	}
	return core.AgentOutput{RunID: input.RunID, Text: output}, nil
}

// RunHybrid continues an existing run – created and partially populated by a
// workflow phase – by executing the autonomous agent.  It does NOT create a
// new RunSnapshot; instead it loads the one already saved for req.RunID,
// updates it on completion.
func (e *Engine) RunHybrid(ctx context.Context, req RunRequest) (RunResult, error) {
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = e.withTimeout(ctx, e.scenario.Runtime.Timeout)
		defer cancel()
	}
	ctx = ContextWithTrustMode(ctx, req.TrustMode)
	ctx = core.ContextWithEpisodeCorrelation(ctx, episodeCorrelationFromRequest(req))
	loaded, err := runstate.LoadAuthorized(ctx, e.persist.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	if loaded.ScenarioName != "" && loaded.ScenarioName != e.scenario.Name {
		return RunResult{}, fmt.Errorf("runtime: run %q belongs to scenario %q, not %q", req.RunID, loaded.ScenarioName, e.scenario.Name)
	}
	if loaded.Status == runstate.RunStatusCompleted {
		return RunResult{}, ErrRunAlreadyCompleted
	}
	if loaded.Status == runstate.RunStatusCancelled {
		return RunResult{}, ErrRunCancelled
	}
	if loaded.Status == runstate.RunStatusPaused {
		return RunResult{}, ErrRunPaused
	}
	if loaded.Status == runstate.RunStatusFailed {
		return RunResult{}, ErrRunFailed
	}
	if episodeCorrelationFromRequest(req).Empty() {
		ctx = core.ContextWithEpisodeCorrelation(ctx, episodeCorrelationFromSnapshot(loaded))
	}
	ctx, runSpan := e.startSpan(ctx, observability.SpanRun,
		observability.Attribute{Key: "run_id", Value: req.RunID},
		observability.Attribute{Key: "scenario_name", Value: e.scenario.Name},
		observability.Attribute{Key: "hybrid", Value: "true"},
	)
	defer runSpan.End()
	agent, agentErr := e.resolveAgent(req.Agent)
	if agentErr != nil {
		return e.failRun(ctx, runSpan, req.RunID, agentErr)
	}
	if len(agent.Policy.OutputSchema) > 0 {
		// See the identical check in Run(): this path also ends in a plain
		// text answer() call, so an output_schema would silently be
		// ignored otherwise.
		err := fmt.Errorf("runtime: agent %q has an output_schema configured; use RunStructured instead of Run", agent.Name)
		return e.failRun(ctx, runSpan, req.RunID, err)
	}
	if e.hasBeforeFinalCheckpoint(agent) && !e.isBeforeFinalResumed(loaded) {
		if e.coord.gate == nil {
			err := fmt.Errorf("runtime: human gate required for configured checkpoint")
			return e.failRun(ctx, runSpan, req.RunID, err)
		}
		result, err := e.pauseBeforeFinalAnswer(ctx, req, agent, &loaded, checkpointPauseOptions{})
		if err != nil {
			return e.failRun(ctx, runSpan, req.RunID, err)
		}
		return result, nil
	}
	output, err := e.answerForAgent(ctx, req, agent)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		return e.failRun(ctx, runSpan, req.RunID, err)
	}
	return e.completeRun(ctx, req.RunID, output)
}

var autonomousPlanSchema = json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","items":{"type":"object","properties":{"goal":{"type":"string"},"tool":{"type":"string"}},"required":["goal"]}}},"required":["steps"]}`)

type autonomousPlan struct {
	Steps []autonomousPlanStep `json:"steps"`
}

type autonomousPlanStep struct {
	Goal string `json:"goal"`
	Tool string `json:"tool,omitempty"`
}
