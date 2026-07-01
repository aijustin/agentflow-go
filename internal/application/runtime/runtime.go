package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type Engine struct {
	scenario               core.Scenario
	llm                    llm.Gateway
	tools                  ToolRegistry
	memory                 map[string]memory.Repository
	tierMemory             map[string]tier.Manager
	cognitive              map[string]memory.CognitiveMemory
	runs                   runstate.Repository
	blobs                  runstate.BlobStore
	events                 core.EventSink
	gate                   core.HumanGate
	policy                 security.Policy
	audit                  audit.Sink
	toolGov                governance.ToolPolicy
	redactor               governance.OutputRedactor
	recorder               observability.Recorder
	tracer                 observability.Tracer
	logger                 log.Logger
	enqueueMemoryReconcile func(context.Context, async.Job) error
}

// Logger is the runtime logging port. Prefer pkg/log.Logger in new code.
type Logger = log.Logger

type ToolRegistry interface {
	ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error)
}

type Dependencies struct {
	LLM            llm.Gateway
	Tools          ToolRegistry
	Memory         map[string]memory.Repository
	TierMemory     map[string]tier.Manager
	Cognitive      map[string]memory.CognitiveMemory
	Runs           runstate.Repository
	Blobs          runstate.BlobStore
	Events         core.EventSink
	HumanGate      core.HumanGate
	Policy         security.Policy
	Audit          audit.Sink
	ToolPolicy     governance.ToolPolicy
	OutputRedactor governance.OutputRedactor
	// Recorder receives metric observations. If nil, metrics are discarded.
	Recorder observability.Recorder
	// Tracer receives distributed tracing spans. If nil, tracing is a no-op.
	Tracer observability.Tracer
	// Logger receives structured log messages for warning and error paths.
	// If nil, messages are silently discarded.
	Logger log.Logger
	// EnqueueMemoryReconcile enqueues async tier reconcile jobs after tier writes.
	EnqueueMemoryReconcile func(context.Context, async.Job) error
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
	return &Engine{
		scenario:               scenario,
		llm:                    deps.LLM,
		tools:                  deps.Tools,
		memory:                 deps.Memory,
		tierMemory:             deps.TierMemory,
		cognitive:              deps.Cognitive,
		runs:                   deps.Runs,
		blobs:                  deps.Blobs,
		events:                 deps.Events,
		gate:                   deps.HumanGate,
		policy:                 deps.Policy,
		audit:                  deps.Audit,
		toolGov:                deps.ToolPolicy,
		redactor:               deps.OutputRedactor,
		recorder:               recorder,
		tracer:                 tracer,
		logger:                 deps.Logger,
		enqueueMemoryReconcile: deps.EnqueueMemoryReconcile,
	}, nil
}

type RunRequest struct {
	RunID   string          `json:"run_id"`
	Agent   string          `json:"agent,omitempty"`
	Prompt  string          `json:"prompt,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
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

	if err := e.beginRun(ctx, &req); err != nil {
		return RunResult{}, err
	}
	failRun := func(err error) (RunResult, error) {
		runSpan.RecordError(err)
		eventType := core.EventRunFailed
		if errors.Is(err, context.Canceled) {
			eventType = core.EventRunCancelled
		}
		e.markRunFailedOrCancelled(ctx, req.RunID, err)
		e.recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal,
			observability.Attribute{Key: "event", Value: string(eventType)},
			observability.Attribute{Key: "scenario", Value: e.scenario.Name})
		return RunResult{}, err
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
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return failRun(err)
	}
	if e.shouldPauseBeforeFinal() && !e.isCheckpointResumed(snapshot) {
		if e.gate == nil {
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
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	if loaded.Status != runstate.RunStatusRunning {
		// A concurrent writer already moved this run to a terminal or
		// paused state (e.g. a tool-loop pause that raced this call, or a
		// cancellation) between answer() returning and this load; do not
		// clobber that state with Completed.
		return nonRunningCompletionResult(req.RunID, loaded.Status)
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRaw := []byte(fmt.Sprintf(`{"text":%q}`, output))
	finalRef, err := e.stepOutputRef(ctx, req.RunID, "final", finalRaw)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	if loaded.StepOutputs == nil {
		loaded.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	e.recordRunCompleted(ctx, loaded)
	e.emit(ctx, core.EventRunCompleted, req.RunID, loaded.StepOutputs["final"].Inline)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: output}, nil
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
	if err := e.beginRun(ctx, &req); err != nil {
		return RunResult{}, err
	}
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	if e.shouldPauseBeforeFinal() {
		if e.gate == nil {
			err := fmt.Errorf("runtime: human gate required for configured checkpoint")
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		snapshot, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
		if err != nil {
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		if !e.isCheckpointResumed(snapshot) {
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
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if loaded.Status != runstate.RunStatusRunning {
		return nonRunningCompletionResult(runID, loaded.Status)
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRef, err := e.stepOutputRef(ctx, runID, "final", raw)
	if err != nil {
		e.markRunFailed(ctx, runID, err)
		return RunResult{}, err
	}
	if loaded.StepOutputs == nil {
		loaded.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	e.recordRunCompleted(ctx, loaded)
	e.emit(ctx, core.EventRunCompleted, runID, finalRef.Inline)
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: string(raw), StructuredOutput: raw}, nil
}

func (e *Engine) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	// This is a static scenario configuration check, not a property of any
	// particular run, so reject it before beginRun creates (and immediately
	// has to fail) a run snapshot for a request that can never succeed.
	if e.shouldPauseBeforeFinal() {
		return nil, fmt.Errorf("runtime: streaming does not support before_final_answer checkpoint; use Run or RunStructured")
	}
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = e.withTimeout(ctx, e.scenario.Runtime.Timeout)
	} else {
		cancel = func() {}
	}
	if err := e.beginRun(ctx, &req); err != nil {
		cancel()
		return nil, err
	}
	source, agent, streamCancel, err := e.streamAnswer(ctx, req)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		cancel()
		return nil, err
	}
	out := make(chan llm.ChatChunk, 1)
	go func() {
		defer close(out)
		defer streamCancel()
		defer cancel()
		sentTerminal := false
		sendTerminal := func(c llm.ChatChunk) {
			if sentTerminal && c.Error == "" {
				return
			}
			sentTerminal = true
			c.Done = true
			select {
			case out <- c:
			case <-ctx.Done():
			}
		}
		send := func(c llm.ChatChunk) bool {
			if sentTerminal {
				return false
			}
			if c.Done {
				sendTerminal(c)
				return true
			}
			select {
			case out <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var b strings.Builder
		for chunk := range source {
			if chunk.Content != "" {
				b.WriteString(chunk.Content)
			}
			if !send(chunk) {
				if err := ctx.Err(); errors.Is(err, context.DeadlineExceeded) {
					e.markRunFailed(ctx, req.RunID, err)
				} else {
					e.markRunCancelled(ctx, req.RunID)
				}
				return
			}
			if chunk.Paused {
				return
			}
			if chunk.Error != "" {
				e.markRunFailed(ctx, req.RunID, errors.New(chunk.Error))
				return
			}
			if chunk.Done {
				if err := e.completeStreamRun(ctx, req.RunID, agent, req.Prompt, b.String()); err != nil {
					sendTerminal(llm.ChatChunk{Error: err.Error()})
				}
				return
			}
		}
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				e.markRunFailed(ctx, req.RunID, err)
			} else {
				e.markRunCancelled(ctx, req.RunID)
			}
			return
		}
		if err := e.completeStreamRun(ctx, req.RunID, agent, req.Prompt, b.String()); err != nil {
			sendTerminal(llm.ChatChunk{Error: err.Error()})
		}
	}()
	return out, nil
}

func (e *Engine) RunAgent(ctx context.Context, agentName string, input core.AgentInput) (core.AgentOutput, error) {
	if err := e.ensureRunActive(ctx, input.RunID); err != nil {
		return core.AgentOutput{}, err
	}
	output, err := e.answer(ctx, RunRequest{
		RunID:   input.RunID,
		Agent:   agentName,
		Prompt:  input.Prompt,
		Context: input.Context,
	})
	if err != nil {
		return core.AgentOutput{}, err
	}
	// answer() only produces a text response, so there is no distinct raw
	// structured output to report; Raw previously echoed input.Context
	// (the caller's own input, not anything the agent generated), which
	// misled callers into treating the input as if it were the output.
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
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
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
	ctx, runSpan := e.startSpan(ctx, observability.SpanRun,
		observability.Attribute{Key: "run_id", Value: req.RunID},
		observability.Attribute{Key: "scenario_name", Value: e.scenario.Name},
		observability.Attribute{Key: "hybrid", Value: "true"},
	)
	defer runSpan.End()
	agent, agentErr := e.resolveAgent(req.Agent)
	if agentErr != nil {
		e.markRunFailed(ctx, req.RunID, agentErr)
		return RunResult{}, agentErr
	}
	if len(agent.Policy.OutputSchema) > 0 {
		// See the identical check in Run(): this path also ends in a plain
		// text answer() call, so an output_schema would silently be
		// ignored otherwise.
		err := fmt.Errorf("runtime: agent %q has an output_schema configured; use RunStructured instead of Run", agent.Name)
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	if e.shouldPauseBeforeFinal() && !e.isCheckpointResumed(loaded) {
		if e.gate == nil {
			err := fmt.Errorf("runtime: human gate required for configured checkpoint")
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		result, err := e.pauseBeforeFinalAnswer(ctx, req, agent, &loaded, checkpointPauseOptions{})
		if err != nil {
			e.markRunFailed(ctx, req.RunID, err)
			return RunResult{}, err
		}
		return result, nil
	}
	output, err := e.answerForAgent(ctx, req, agent)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		e.markRunFailedOrCancelled(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err = runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	if loaded.Status != runstate.RunStatusRunning {
		return nonRunningCompletionResult(req.RunID, loaded.Status)
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRaw := []byte(fmt.Sprintf(`{"text":%q}`, output))
	finalRef, err := e.stepOutputRef(ctx, req.RunID, "final", finalRaw)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	if loaded.StepOutputs == nil {
		loaded.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	e.recordRunCompleted(ctx, loaded)
	e.emit(ctx, core.EventRunCompleted, req.RunID, finalRef.Inline)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

var autonomousPlanSchema = json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","items":{"type":"object","properties":{"goal":{"type":"string"},"tool":{"type":"string"}},"required":["goal"]}}},"required":["steps"]}`)

type autonomousPlan struct {
	Steps []autonomousPlanStep `json:"steps"`
}

type autonomousPlanStep struct {
	Goal string `json:"goal"`
	Tool string `json:"tool,omitempty"`
}
