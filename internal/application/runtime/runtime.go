package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ctx, cancel := e.withTimeout(ctx, e.scenario.Runtime.Timeout)
	defer cancel()
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	ctx, runSpan := e.startSpan(ctx, observability.SpanRun,
		observability.Attribute{Key: "run_id", Value: req.RunID},
		observability.Attribute{Key: "scenario_name", Value: e.scenario.Name},
	)
	defer runSpan.End()

	runStart := time.Now()
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
		return RunResult{}, err
	}
	e.emit(ctx, core.EventRunStarted, req.RunID, nil)

	agent, agentErr := e.resolveAgent(req.Agent)
	if agentErr != nil {
		return RunResult{}, agentErr
	}
	checkpointVars := map[string]json.RawMessage{
		checkpointPromptVar:  json.RawMessage(fmt.Sprintf("%q", req.Prompt)),
		checkpointAgentVar:   json.RawMessage(fmt.Sprintf("%q", agent.Name)),
		checkpointContextVar: req.Context,
	}
	if e.shouldPauseBeforeFinal() {
		if e.gate == nil {
			return RunResult{}, fmt.Errorf("runtime: human gate required for configured checkpoint")
		}
		checkpointVars[checkpointKindVar] = json.RawMessage(`"before_final_answer"`)
		if saveErr := e.saveCheckpointVariables(ctx, &snapshot, checkpointVars); saveErr != nil {
			return RunResult{}, saveErr
		}
		loaded, loadErr := e.runs.Load(ctx, req.RunID)
		if loadErr != nil {
			return RunResult{}, loadErr
		}
		snapshot = loaded
		state := core.CheckpointState{
			RunID:   req.RunID,
			Version: snapshot.Version,
			NodeID:  "before_final_answer",
			Payload: []byte(fmt.Sprintf(`{"prompt":%q,"agent":%q}`, req.Prompt, agent.Name)),
		}
		token, err := e.gate.Pause(ctx, state)
		if err != nil {
			return RunResult{}, err
		}
		e.emit(ctx, core.EventRunPaused, req.RunID, state.Payload)
		return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: token}, nil
	}

	output, err := e.answer(ctx, req)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		runSpan.RecordError(err)
		e.markRunFailed(ctx, req.RunID, err)
		e.recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal,
			observability.Attribute{Key: "event", Value: string(core.EventRunFailed)},
			observability.Attribute{Key: "scenario", Value: e.scenario.Name})
		return RunResult{}, err
	}
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRaw := []byte(fmt.Sprintf(`{"text":%q}`, output))
	finalRef, err := e.stepOutputRef(ctx, req.RunID, "final", finalRaw)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	e.recorder.ObserveHistogram(ctx, observability.MetricRunDurationSeconds, time.Since(runStart).Seconds(),
		observability.Attribute{Key: "scenario", Value: e.scenario.Name})
	e.recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal,
		observability.Attribute{Key: "event", Value: string(core.EventRunCompleted)},
		observability.Attribute{Key: "scenario", Value: e.scenario.Name})
	e.emit(ctx, core.EventRunCompleted, req.RunID, loaded.StepOutputs["final"].Inline)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

func (e *Engine) RunStructured(ctx context.Context, req RunRequest) (RunResult, error) {
	ctx, cancel := e.withTimeout(ctx, e.scenario.Runtime.Timeout)
	defer cancel()
	if _, err := e.beginRun(ctx, &req); err != nil {
		return RunResult{}, err
	}
	raw, err := e.structuredAnswer(ctx, req)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	loaded.Status = runstate.RunStatusCompleted
	finalRef, err := e.stepOutputRef(ctx, req.RunID, "final", raw)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded.StepOutputs["final"] = finalRef
	if err := e.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	e.emit(ctx, core.EventRunCompleted, req.RunID, finalRef.Inline)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: string(raw), StructuredOutput: raw}, nil
}

func (e *Engine) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	ctx, cancel := e.withTimeout(ctx, e.scenario.Runtime.Timeout)
	if _, err := e.beginRun(ctx, &req); err != nil {
		cancel()
		return nil, err
	}
	source, agent, streamCancel, err := e.streamAnswer(ctx, req)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		cancel()
		return nil, err
	}
	out := make(chan llm.ChatChunk)
	go func() {
		defer close(out)
		defer streamCancel()
		defer cancel()
		var b strings.Builder
		for chunk := range source {
			if chunk.Content != "" {
				b.WriteString(chunk.Content)
			}
			out <- chunk
			if chunk.Error != "" {
				e.markRunFailed(ctx, req.RunID, errors.New(chunk.Error))
				return
			}
			if chunk.Done {
				if err := e.completeStreamRun(ctx, req.RunID, agent, req.Prompt, b.String()); err != nil {
					out <- llm.ChatChunk{Done: true, Error: err.Error()}
				}
				return
			}
		}
		if err := e.completeStreamRun(ctx, req.RunID, agent, req.Prompt, b.String()); err != nil {
			out <- llm.ChatChunk{Done: true, Error: err.Error()}
		}
	}()
	return out, nil
}

func (e *Engine) RunAgent(ctx context.Context, agentName string, input core.AgentInput) (core.AgentOutput, error) {
	output, err := e.answer(ctx, RunRequest{
		RunID:   input.RunID,
		Agent:   agentName,
		Prompt:  input.Prompt,
		Context: input.Context,
	})
	if err != nil {
		return core.AgentOutput{}, err
	}
	return core.AgentOutput{RunID: input.RunID, Text: output, Raw: input.Context}, nil
}

// RunHybrid continues an existing run – created and partially populated by a
// workflow phase – by executing the autonomous agent.  It does NOT create a
// new RunSnapshot; instead it loads the one already saved for req.RunID,
// updates it on completion.
func (e *Engine) RunHybrid(ctx context.Context, req RunRequest) (RunResult, error) {
	ctx, cancel := e.withTimeout(ctx, e.scenario.Runtime.Timeout)
	defer cancel()
	loaded, err := runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	if loaded.Status == runstate.RunStatusCompleted {
		return RunResult{}, ErrRunAlreadyCompleted
	}
	if loaded.Status == runstate.RunStatusCancelled {
		return RunResult{}, ErrRunCancelled
	}
	output, err := e.answer(ctx, req)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err = runstate.LoadAuthorized(ctx, e.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
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
