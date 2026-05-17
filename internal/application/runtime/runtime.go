package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type Engine struct {
	scenario core.Scenario
	llm      llm.Gateway
	tools    ToolRegistry
	memory   map[string]memory.Repository
	runs     runstate.Repository
	blobs    runstate.BlobStore
	events   core.EventSink
	gate     core.HumanGate
	policy   security.Policy
	audit    audit.Sink
	toolGov  governance.ToolPolicy
	redactor governance.OutputRedactor
}

type ToolRegistry interface {
	ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error)
}

type Dependencies struct {
	LLM            llm.Gateway
	Tools          ToolRegistry
	Memory         map[string]memory.Repository
	Runs           runstate.Repository
	Blobs          runstate.BlobStore
	Events         core.EventSink
	HumanGate      core.HumanGate
	Policy         security.Policy
	Audit          audit.Sink
	ToolPolicy     governance.ToolPolicy
	OutputRedactor governance.OutputRedactor
}

func NewEngine(scenario core.Scenario, deps Dependencies) (*Engine, error) {
	if deps.Runs == nil {
		return nil, fmt.Errorf("runtime: runstate repository is required")
	}
	if deps.Events == nil {
		deps.Events = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	}
	return &Engine{
		scenario: scenario,
		llm:      deps.LLM,
		tools:    deps.Tools,
		memory:   deps.Memory,
		runs:     deps.Runs,
		blobs:    deps.Blobs,
		events:   deps.Events,
		gate:     deps.HumanGate,
		policy:   deps.Policy,
		audit:    deps.Audit,
		toolGov:  deps.ToolPolicy,
		redactor: deps.OutputRedactor,
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
		req.RunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
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
	if err := e.runs.Save(ctx, &snapshot, 0); err != nil {
		return RunResult{}, err
	}
	e.emit(ctx, core.EventRunStarted, req.RunID, nil)

	if e.shouldPauseBeforeFinal() {
		if e.gate == nil {
			return RunResult{}, fmt.Errorf("runtime: human gate required for configured checkpoint")
		}
		state := core.CheckpointState{
			RunID:   req.RunID,
			Version: snapshot.Version,
			NodeID:  "before_final_answer",
			Payload: []byte(fmt.Sprintf(`{"prompt":%q}`, req.Prompt)),
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
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err := e.runs.Load(ctx, req.RunID)
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
	e.emit(ctx, core.EventRunCompleted, req.RunID, loaded.StepOutputs["final"].Inline)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

func (e *Engine) RunStructured(ctx context.Context, req RunRequest) (RunResult, error) {
	ctx, cancel := e.withTimeout(ctx, e.scenario.Runtime.Timeout)
	defer cancel()
	if req.RunID == "" {
		req.RunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: e.scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables:    map[string]json.RawMessage{"input": req.Context},
		StepOutputs:  make(map[string]runstate.StepOutputRef),
	}
	if err := e.runs.Save(ctx, &snapshot, 0); err != nil {
		return RunResult{}, err
	}
	e.emit(ctx, core.EventRunStarted, req.RunID, nil)
	raw, err := e.structuredAnswer(ctx, req)
	if err != nil {
		e.markRunFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err := e.runs.Load(ctx, req.RunID)
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
	if req.RunID == "" {
		req.RunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: e.scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables:    map[string]json.RawMessage{"input": req.Context},
		StepOutputs:  make(map[string]runstate.StepOutputRef),
	}
	if err := e.runs.Save(ctx, &snapshot, 0); err != nil {
		cancel()
		return nil, err
	}
	e.emit(ctx, core.EventRunStarted, req.RunID, nil)
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

func (e *Engine) answer(ctx context.Context, req RunRequest) (string, error) {
	if e.llm == nil {
		return req.Prompt, nil
	}
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return "", err
	}
	ctx, cancel := e.withTimeout(ctx, agent.Policy.Timeout)
	defer cancel()
	profile := e.scenario.LLMs[agent.LLM]
	history, err := e.readMemory(ctx, req.RunID, agent)
	if err != nil {
		return "", err
	}
	messages, stats := e.prepareContext(agent, profile, req, history)
	if e.scenario.Orchestration.Planning.Enabled {
		var err error
		messages, err = e.injectAutonomousPlan(ctx, req.RunID, agent, profile, req, messages)
		if err != nil {
			return "", err
		}
	}
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	if len(agent.Tools)+len(agent.SubAgents) > 0 && e.llm.Supports(agent.LLM, llm.CapToolCall) {
		if caller, ok := e.llm.(llm.ToolCaller); ok {
			output, err := e.answerWithTools(ctx, req.RunID, agent, profile, baseReq, caller)
			if err != nil {
				return "", err
			}
			if err := e.writeMemory(ctx, req.RunID, agent, []memoryMessage{
				{Role: string(llm.RoleUser), Content: req.Prompt},
				{Role: string(llm.RoleAssistant), Content: output},
			}); err != nil {
				return "", err
			}
			return output, nil
		}
	}
	e.emitJSON(ctx, core.EventContextPrepared, req.RunID, stats)
	resp, err := e.chatWithRetry(ctx, req.RunID, agent, profile, baseReq)
	if err != nil {
		return "", err
	}
	if resp.Usage.TotalTokens > 0 {
		e.emitJSON(ctx, core.EventLLMTokenUsage, req.RunID, resp.Usage)
	}
	if strings.TrimSpace(resp.Message.Content) == "" && resp.FinishReason == "length" {
		return "", fmt.Errorf("runtime: llm response was empty after reaching max tokens; increase max_output_tokens or disable reasoning output for profile %q", agent.LLM)
	}
	if err := e.writeMemory(ctx, req.RunID, agent, []memoryMessage{
		{Role: string(llm.RoleUser), Content: req.Prompt},
		{Role: string(llm.RoleAssistant), Content: resp.Message.Content},
	}); err != nil {
		return "", err
	}
	return resp.Message.Content, nil
}

var autonomousPlanSchema = json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","items":{"type":"object","properties":{"goal":{"type":"string"},"tool":{"type":"string"}},"required":["goal"]}}},"required":["steps"]}`)

type autonomousPlan struct {
	Steps []autonomousPlanStep `json:"steps"`
}

type autonomousPlanStep struct {
	Goal string `json:"goal"`
	Tool string `json:"tool,omitempty"`
}

func (e *Engine) injectAutonomousPlan(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req RunRequest, messages []llm.Message) ([]llm.Message, error) {
	plannerAgent := agent
	if planner := e.scenario.Orchestration.Planning.Agent; planner != "" {
		resolved, err := e.resolveAgent(planner)
		if err != nil {
			return nil, err
		}
		plannerAgent = resolved
		profile = e.scenario.LLMs[plannerAgent.LLM]
	}
	maxSteps := firstPositive(e.scenario.Orchestration.Planning.MaxSteps, agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 5)
	planReq := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: fmt.Sprintf("Create a concise execution plan with at most %d steps. Return JSON with a steps array; each step has goal and optional tool.", maxSteps)},
			{Role: llm.RoleUser, Content: req.Prompt},
		},
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	var rawPlan []byte
	if outputter, ok := e.llm.(llm.StructuredOutputter); ok && e.llm.Supports(plannerAgent.LLM, llm.CapStructuredOutput) {
		raw, err := e.structuredWithRetry(ctx, runID, plannerAgent, profile, autonomousPlanSchema, planReq, outputter)
		if err != nil {
			return nil, err
		}
		rawPlan = raw
	} else {
		resp, err := e.chatWithRetry(ctx, runID, plannerAgent, profile, planReq)
		if err != nil {
			return nil, err
		}
		rawPlan = []byte(resp.Message.Content)
	}
	planText := formatAutonomousPlan(rawPlan, maxSteps)
	if strings.TrimSpace(planText) == "" {
		return messages, nil
	}
	planned := make([]llm.Message, 0, len(messages)+1)
	planned = append(planned, llm.Message{Role: llm.RoleSystem, Content: "Autonomous execution plan:\n" + planText})
	planned = append(planned, messages...)
	e.emitJSON(ctx, core.EventContextPrepared, runID, map[string]any{"planning": true, "steps": strings.Count(planText, "\n") + 1})
	return planned, nil
}

func formatAutonomousPlan(raw []byte, maxSteps int) string {
	var plan autonomousPlan
	if err := json.Unmarshal(raw, &plan); err != nil || len(plan.Steps) == 0 {
		return strings.TrimSpace(string(raw))
	}
	limit := len(plan.Steps)
	if maxSteps > 0 && limit > maxSteps {
		limit = maxSteps
	}
	var b strings.Builder
	for index := 0; index < limit; index++ {
		step := plan.Steps[index]
		goal := strings.TrimSpace(step.Goal)
		if goal == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(index + 1))
		b.WriteString(". ")
		b.WriteString(goal)
		if step.Tool != "" {
			b.WriteString(" (tool: ")
			b.WriteString(step.Tool)
			b.WriteByte(')')
		}
	}
	return b.String()
}

func (e *Engine) structuredAnswer(ctx context.Context, req RunRequest) (json.RawMessage, error) {
	if e.llm == nil {
		return nil, fmt.Errorf("runtime: structured output requires llm gateway")
	}
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return nil, err
	}
	if len(agent.Policy.OutputSchema) == 0 {
		return nil, fmt.Errorf("runtime: agent %q output_schema is required for structured output", agent.Name)
	}
	if !json.Valid(agent.Policy.OutputSchema) {
		return nil, fmt.Errorf("runtime: agent %q output_schema is invalid JSON", agent.Name)
	}
	outputter, ok := e.llm.(llm.StructuredOutputter)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapStructuredOutput) {
		return nil, fmt.Errorf("runtime: llm profile %q does not support structured output", agent.LLM)
	}
	ctx, cancel := e.withTimeout(ctx, agent.Policy.Timeout)
	defer cancel()
	profile := e.scenario.LLMs[agent.LLM]
	history, err := e.readMemory(ctx, req.RunID, agent)
	if err != nil {
		return nil, err
	}
	messages, stats := e.prepareContext(agent, profile, req, history)
	e.emitJSON(ctx, core.EventContextPrepared, req.RunID, stats)
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	raw, err := e.structuredWithRetry(ctx, req.RunID, agent, profile, agent.Policy.OutputSchema, baseReq, outputter)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("runtime: structured output was not valid JSON")
	}
	if err := e.writeMemory(ctx, req.RunID, agent, []memoryMessage{
		{Role: string(llm.RoleUser), Content: req.Prompt},
		{Role: string(llm.RoleAssistant), Content: string(raw)},
	}); err != nil {
		return nil, err
	}
	return raw, nil
}

func (e *Engine) streamAnswer(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, core.Agent, context.CancelFunc, error) {
	if e.llm == nil {
		return nil, core.Agent{}, nil, fmt.Errorf("runtime: streaming requires llm gateway")
	}
	agent, err := e.resolveAgent(req.Agent)
	if err != nil {
		return nil, core.Agent{}, nil, err
	}
	ctx, cancel := e.withTimeout(ctx, agent.Policy.Timeout)
	profile := e.scenario.LLMs[agent.LLM]
	history, err := e.readMemory(ctx, req.RunID, agent)
	if err != nil {
		cancel()
		return nil, core.Agent{}, nil, err
	}
	messages, stats := e.prepareContext(agent, profile, req, history)
	if e.scenario.Orchestration.Planning.Enabled {
		messages, err = e.injectAutonomousPlan(ctx, req.RunID, agent, profile, req, messages)
		if err != nil {
			cancel()
			return nil, core.Agent{}, nil, err
		}
	}
	e.emitJSON(ctx, core.EventContextPrepared, req.RunID, stats)
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	if len(agent.Tools)+len(agent.SubAgents) > 0 {
		caller, ok := e.llm.(llm.ToolCaller)
		if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
			cancel()
			return nil, core.Agent{}, nil, fmt.Errorf("runtime: llm profile %q does not support tool calling", agent.LLM)
		}
		ch := make(chan llm.ChatChunk, 1)
		go func() {
			defer close(ch)
			output, err := e.answerWithTools(ctx, req.RunID, agent, profile, baseReq, caller)
			if err != nil {
				ch <- llm.ChatChunk{Done: true, Error: err.Error()}
				return
			}
			ch <- llm.ChatChunk{Content: output, Done: true}
		}()
		return ch, agent, cancel, nil
	}
	streamer, ok := e.llm.(llm.Streamer)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapStream) {
		cancel()
		return nil, core.Agent{}, nil, fmt.Errorf("runtime: llm profile %q does not support streaming", agent.LLM)
	}
	e.emitJSON(ctx, core.EventLLMCalled, req.RunID, map[string]any{"profile": agent.LLM, "stream": true})
	ch, err := streamer.StreamChat(ctx, agent.LLM, baseReq)
	if err != nil {
		cancel()
		return nil, core.Agent{}, nil, err
	}
	return ch, agent, cancel, nil
}

func (e *Engine) answerWithTools(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ChatRequest, caller llm.ToolCaller) (string, error) {
	maxSteps := firstPositive(agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 8)
	toolSpecs := e.toolSpecs(agent)
	messages := append([]llm.Message(nil), req.Messages...)
	toolCalls := make(map[string]int)
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		prepared, stats := e.prepareMessages(messages, profile)
		e.emitJSON(ctx, core.EventContextPrepared, runID, stats)
		toolReq := llm.ToolCallRequest{
			ChatRequest: llm.ChatRequest{
				Messages:        prepared,
				Temperature:     req.Temperature,
				TopP:            req.TopP,
				MaxTokens:       req.MaxTokens,
				Thinking:        req.Thinking,
				ReasoningEffort: req.ReasoningEffort,
				ExtraBody:       req.ExtraBody,
			},
			Tools: toolSpecs,
		}
		resp, err := e.chatWithToolsWithRetry(ctx, runID, agent, profile, toolReq, caller, step+1)
		if err != nil {
			return "", err
		}
		if resp.Usage.TotalTokens > 0 {
			e.emitJSON(ctx, core.EventLLMTokenUsage, runID, resp.Usage)
		}
		assistant := resp.Message
		assistant.Role = llm.RoleAssistant
		assistant.ToolCalls = resp.ToolCalls
		messages = append(messages, assistant)
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Message.Content) == "" && resp.FinishReason == "length" {
				return "", fmt.Errorf("runtime: llm response was empty after reaching max tokens; increase max_output_tokens or disable reasoning output for profile %q", agent.LLM)
			}
			return resp.Message.Content, nil
		}
		for _, toolCall := range resp.ToolCalls {
			result := e.dispatchTool(ctx, runID, agent, toolCall, toolCalls)
			contextResult := compactToolResultForContext(result, profile.Context.ToolResultMaxTokens)
			raw, err := json.Marshal(contextResult)
			if err != nil {
				return "", err
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    string(raw),
				Name:       toolCall.Name,
				ToolCallID: toolCall.ID,
			})
		}
	}
	return "", fmt.Errorf("runtime: autonomous tool loop exceeded max_steps=%d", maxSteps)
}

func compactToolResultForContext(result core.ToolResult, maxTokens int) core.ToolResult {
	if maxTokens <= 0 {
		return result
	}
	raw, err := json.Marshal(result)
	if err != nil || contextwindow.EstimateTokens(string(raw)) <= maxTokens {
		return result
	}
	content := string(result.Output)
	if content == "" {
		content = result.Error
	}
	compact := map[string]any{
		"truncated":       true,
		"original_tokens": contextwindow.EstimateTokens(string(raw)),
		"max_tokens":      maxTokens,
		"content":         truncateForTokenBudget(content, maxTokens),
	}
	output, err := json.Marshal(compact)
	if err != nil {
		return result
	}
	return core.ToolResult{Tool: result.Tool, Output: output, Error: result.Error}
}

func truncateForTokenBudget(content string, maxTokens int) string {
	if maxTokens <= 0 || contextwindow.EstimateTokens(content) <= maxTokens {
		return content
	}
	limit := maxTokens * 3
	runes := []rune(content)
	if limit <= 0 || len(runes) <= limit {
		return content
	}
	return string(runes[:limit]) + "..."
}

func (e *Engine) prepareContext(agent core.Agent, profile core.LLMProfileRef, req RunRequest, history []llm.Message) ([]llm.Message, contextwindow.Stats) {
	raw := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: agent.Instructions},
	}
	for _, msg := range history {
		raw = append(raw, contextwindow.Message{
			Role:       contextwindow.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   msg.Metadata,
		})
	}
	if len(req.Context) > 0 && string(req.Context) != "null" {
		raw = append(raw, contextwindow.Message{
			Role:    contextwindow.RoleUser,
			Content: "Runtime context JSON:\n" + string(req.Context),
			Metadata: map[string]string{
				"priority": "context",
			},
		})
	}
	raw = append(raw, contextwindow.Message{Role: contextwindow.RoleUser, Content: req.Prompt})
	return e.prepareRawMessages(raw, profile)
}

func (e *Engine) prepareMessages(messages []llm.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	raw := make([]contextwindow.Message, 0, len(messages))
	for i, msg := range messages {
		metadata := cloneMetadata(msg.Metadata)
		metadata["source_index"] = strconv.Itoa(i)
		raw = append(raw, contextwindow.Message{
			Role:       contextwindow.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   metadata,
		})
	}
	prepared, stats := e.prepareRawMessages(raw, profile)
	for i := range prepared {
		sourceIndex, ok := prepared[i].Metadata["source_index"]
		if ok {
			delete(prepared[i].Metadata, "source_index")
		}
		if index, err := strconv.Atoi(sourceIndex); ok && err == nil && index >= 0 && index < len(messages) {
			prepared[i].ToolCalls = messages[index].ToolCalls
		}
	}
	return prepared, stats
}

func (e *Engine) prepareRawMessages(raw []contextwindow.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	policy := profile.Context
	if policy.ContextWindowTokens == 0 {
		policy.ContextWindowTokens = profile.ContextWindowTokens
	}
	if policy.ReservedOutputTokens == 0 {
		policy.ReservedOutputTokens = profile.MaxOutputTokens
	}
	result := contextwindow.New(policy).Prepare(raw)
	messages := make([]llm.Message, 0, len(result.Messages))
	for _, msg := range result.Messages {
		messages = append(messages, llm.Message{
			Role:       llm.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   msg.Metadata,
		})
	}
	return messages, result.Stats
}

func (e *Engine) toolSpecs(agent core.Agent) []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(agent.Tools)+len(agent.SubAgents))
	for _, name := range agent.Tools {
		tool, ok := e.scenario.Tools[name]
		if !ok {
			continue
		}
		specs = append(specs, llm.ToolSpec{
			Name:        name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
		})
	}
	for _, name := range agent.SubAgents {
		sub, ok := e.scenario.Agents[name]
		if !ok {
			continue
		}
		description := sub.Description
		if description == "" {
			description = "Delegate a task to sub-agent " + name
		}
		specs = append(specs, llm.ToolSpec{
			Name:        delegateToolName(name),
			Description: description,
			Schema:      json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"context":{"type":"object"}},"required":["prompt"]}`),
		})
	}
	return specs
}

func (e *Engine) dispatchTool(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, callCounts map[string]int) core.ToolResult {
	if subAgentName, ok := e.delegateTarget(agent, call.Name); ok {
		resource := toolResource(agent, call, nil)
		if err := e.authorizeTool(ctx, runID, resource); err != nil {
			result := core.ToolResult{Tool: call.Name, Error: "tool invocation unauthorized"}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
			return result
		}
		return e.dispatchSubAgent(ctx, runID, agent, subAgentName, call)
	}
	if !agentAllowsTool(agent, call.Name) {
		result := core.ToolResult{Tool: call.Name, Error: "tool is not in agent whitelist"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	tool, ok := e.scenario.Tools[call.Name]
	if !ok {
		result := core.ToolResult{Tool: call.Name, Error: "tool is not declared in scenario"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	if tool.Name == "" {
		tool.Name = call.Name
	}
	if reason := approvalDenialReason(tool); reason != "" {
		result := core.ToolResult{Tool: call.Name, Error: reason}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": reason})
		return result
	}
	if tool.RateCap > 0 && callCounts[call.Name] >= tool.RateCap {
		result := core.ToolResult{Tool: call.Name, Error: fmt.Sprintf("tool rate cap exceeded: %d call(s) per run", tool.RateCap)}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	if e.tools == nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool executor registry is not configured"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	resource := toolResource(agent, call, &tool)
	if err := e.authorizeTool(ctx, runID, resource); err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool invocation unauthorized"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
		return result
	}
	if err := e.authorizeGovernanceTool(ctx, runID, agent, tool, call, callCounts); err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "tool invocation blocked by governance"}
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: err.Error()})
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": err.Error()})
		return result
	}
	executor, ok, err := e.tools.ResolveTool(ctx, tool)
	if err != nil {
		result := core.ToolResult{Tool: call.Name, Error: "resolve tool executor: " + err.Error()}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	if !ok {
		result := core.ToolResult{Tool: call.Name, Error: "tool executor is not registered"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	callCounts[call.Name]++
	result, err := e.executeToolWithRetry(ctx, runID, agent, call, executor)
	if err != nil {
		result = core.ToolResult{Tool: call.Name, Error: err.Error()}
	}
	if err := e.saveStepOutput(ctx, runID, "tool."+call.ID, result); err != nil && result.Error == "" {
		result.Error = "persist tool output: " + err.Error()
	}
	e.recordAudit(ctx, audit.Event{Type: audit.EventToolInvoked, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: toolOutcome(result)})
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "error": result.Error})
	_ = e.writeMemory(ctx, runID, agent, []memoryMessage{{
		Role:       string(llm.RoleTool),
		Content:    string(mustMarshal(result)),
		Tool:       call.Name,
		ToolCallID: call.ID,
	}})
	return result
}

func (e *Engine) authorizeGovernanceTool(ctx context.Context, runID string, agent core.Agent, tool core.Tool, call llm.ToolCall, callCounts map[string]int) error {
	if e.toolGov == nil {
		return nil
	}
	return e.toolGov.AuthorizeTool(ctx, governance.ToolInvocation{
		RunID:      runID,
		Agent:      agent.Name,
		Tool:       call.Name,
		SideEffect: tool.SideEffect,
		Input:      call.Input,
		CallCount:  callCounts[call.Name],
		TotalCalls: totalToolCalls(callCounts),
		Metadata:   cloneStringMap(tool.Metadata),
	})
}

func totalToolCalls(callCounts map[string]int) int {
	total := 0
	for _, count := range callCounts {
		total += count
	}
	return total
}

func (e *Engine) authorizeTool(ctx context.Context, runID string, resource security.Resource) error {
	if e.policy == nil {
		return nil
	}
	principal, err := identity.RequirePrincipal(ctx)
	if err != nil {
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: identity.Principal{}, Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: security.ErrUnauthenticated.Error()})
		return security.ErrUnauthenticated
	}
	if err := e.policy.Authorize(ctx, principal, security.ActionToolInvoke, resource); err != nil {
		e.recordAudit(ctx, audit.Event{Type: audit.EventPolicyDenied, Principal: principal, Action: security.ActionToolInvoke, Resource: resource, RunID: runID, Outcome: "denied", Reason: err.Error()})
		return err
	}
	return nil
}

func (e *Engine) recordAudit(ctx context.Context, event audit.Event) {
	if e.audit == nil {
		return
	}
	_ = e.audit.Record(ctx, event.WithDefaults(time.Now().UTC()))
}

func principalFromContext(ctx context.Context) identity.Principal {
	principal, _ := identity.PrincipalFromContext(ctx)
	return principal
}

func toolResource(agent core.Agent, call llm.ToolCall, tool *core.Tool) security.Resource {
	resource := security.Resource{Type: "tool", ID: call.Name, Metadata: map[string]string{"agent": agent.Name}}
	if tool != nil {
		resource.Metadata["tool_type"] = tool.Type
		resource.Metadata["side_effect"] = string(tool.SideEffect)
	}
	return resource
}

func toolOutcome(result core.ToolResult) string {
	if result.Error != "" {
		return "error"
	}
	return "success"
}

func (e *Engine) dispatchSubAgent(ctx context.Context, runID string, parent core.Agent, subAgentName string, call llm.ToolCall) core.ToolResult {
	var input struct {
		Prompt  string          `json:"prompt"`
		Context json.RawMessage `json:"context"`
	}
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &input); err != nil {
			result := core.ToolResult{Tool: call.Name, Error: "invalid delegation input: " + err.Error()}
			e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "reason": result.Error})
			return result
		}
	}
	if strings.TrimSpace(input.Prompt) == "" {
		result := core.ToolResult{Tool: call.Name, Error: "delegation prompt is required"}
		e.emitJSON(ctx, core.EventToolDenied, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "reason": result.Error})
		return result
	}
	e.emitJSON(ctx, core.EventToolCalled, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "sub_agent": subAgentName, "tool_call_id": call.ID})
	output, err := e.answer(ctx, RunRequest{RunID: runID, Agent: subAgentName, Prompt: input.Prompt, Context: input.Context})
	result := core.ToolResult{Tool: call.Name}
	if err != nil {
		result.Error = err.Error()
	} else {
		raw, marshalErr := json.Marshal(core.AgentOutput{RunID: runID, Text: output})
		if marshalErr != nil {
			result.Error = marshalErr.Error()
		} else {
			result.Output = raw
		}
	}
	if err := e.saveStepOutput(ctx, runID, "agent."+subAgentName+"."+call.ID, result); err != nil && result.Error == "" {
		result.Error = "persist delegated output: " + err.Error()
	}
	e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": parent.Name, "tool": call.Name, "sub_agent": subAgentName, "tool_call_id": call.ID, "error": result.Error})
	return result
}

func (e *Engine) chatWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ChatRequest) (llm.ChatResponse, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return llm.ChatResponse{}, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, map[string]any{"profile": agent.LLM, "tools": false, "attempt": attempt})
		resp, err := e.llm.Chat(callCtx, agent.LLM, req)
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, map[string]any{"profile": agent.LLM, "finish_reason": resp.FinishReason, "attempt": attempt})
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return llm.ChatResponse{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return llm.ChatResponse{}, err
		}
	}
	return llm.ChatResponse{}, lastErr
}

func (e *Engine) chatWithToolsWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ToolCallRequest, caller llm.ToolCaller, step int) (llm.ToolCallResponse, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return llm.ToolCallResponse{}, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, map[string]any{"profile": agent.LLM, "tools": true, "step": step, "attempt": attempt})
		resp, err := caller.ChatWithTools(callCtx, agent.LLM, req)
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, map[string]any{"profile": agent.LLM, "finish_reason": resp.FinishReason, "tool_calls": len(resp.ToolCalls), "step": step, "attempt": attempt})
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return llm.ToolCallResponse{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return llm.ToolCallResponse{}, err
		}
	}
	return llm.ToolCallResponse{}, lastErr
}

func (e *Engine) structuredWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, schema json.RawMessage, req llm.ChatRequest, outputter llm.StructuredOutputter) (json.RawMessage, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, map[string]any{"profile": agent.LLM, "structured": true, "attempt": attempt})
		raw, err := outputter.StructuredChat(callCtx, agent.LLM, schema, req)
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, map[string]any{"profile": agent.LLM, "structured": true, "attempt": attempt})
			return raw, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return nil, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (e *Engine) executeToolWithRetry(ctx context.Context, runID string, agent core.Agent, call llm.ToolCall, executor core.ToolExecutor) (core.ToolResult, error) {
	attempts := e.maxAttempts(agent)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, err
		}
		e.emitJSON(ctx, core.EventToolCalled, runID, map[string]any{"agent": agent.Name, "tool": call.Name, "tool_call_id": call.ID, "attempt": attempt})
		result, err := executor.Execute(ctx, core.ToolCall{RunID: runID, Agent: agent.Name, Tool: call.Name, Input: call.Input})
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			return core.ToolResult{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			return core.ToolResult{}, err
		}
	}
	return core.ToolResult{}, lastErr
}

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
	if err := e.writeMemory(ctx, runID, agent, []memoryMessage{
		{Role: string(llm.RoleUser), Content: prompt},
		{Role: string(llm.RoleAssistant), Content: output},
	}); err != nil {
		return err
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
	snapshot, err := e.runs.Load(ctx, runID)
	if err != nil {
		return err
	}
	if snapshot.StepOutputs == nil {
		snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ref, err := e.stepOutputRef(ctx, runID, key, raw)
	if err != nil {
		return err
	}
	snapshot.StepOutputs[key] = ref
	return e.runs.Save(ctx, &snapshot, snapshot.Version)
}

func approvalDenialReason(tool core.Tool) string {
	switch tool.Approval {
	case "", core.ApprovalNever:
		return ""
	case core.ApprovalAlways:
		return "tool requires approval"
	case core.ApprovalRisky:
		switch tool.SideEffect {
		case core.SideEffectWrite, core.SideEffectExternal, core.SideEffectDangerous:
			return "risky tool requires approval"
		default:
			return ""
		}
	default:
		return fmt.Sprintf("unsupported approval policy %q", tool.Approval)
	}
}

func agentAllowsTool(agent core.Agent, tool string) bool {
	for _, allowed := range agent.Tools {
		if allowed == tool {
			return true
		}
	}
	return false
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
		_ = e.runs.Save(persistCtx, &snapshot, snapshot.Version)
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

type memoryMessage struct {
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Time       time.Time `json:"time"`
}

func (e *Engine) readMemory(ctx context.Context, runID string, agent core.Agent) ([]llm.Message, error) {
	repo, ns, ok := e.memoryRepository(runID, agent)
	if !ok {
		return nil, nil
	}
	raw, err := repo.Get(ctx, ns, "messages")
	if err != nil {
		if err == memory.ErrNotFound {
			e.emitJSON(ctx, core.EventMemoryRead, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": 0})
			return nil, nil
		}
		return nil, err
	}
	var stored []memoryMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("runtime: memory %q messages are invalid: %w", agent.Memory, err)
	}
	messages := make([]llm.Message, 0, len(stored))
	for _, msg := range stored {
		switch llm.Role(msg.Role) {
		case llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
			messages = append(messages, llm.Message{
				Role:       llm.Role(msg.Role),
				Content:    msg.Content,
				Name:       msg.Tool,
				ToolCallID: msg.ToolCallID,
				Metadata: map[string]string{
					"memory": agent.Memory,
				},
			})
		}
	}
	e.emitJSON(ctx, core.EventMemoryRead, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages)})
	return messages, nil
}

func (e *Engine) writeMemory(ctx context.Context, runID string, agent core.Agent, messages []memoryMessage) error {
	repo, ns, ok := e.memoryRepository(runID, agent)
	if !ok || len(messages) == 0 {
		return nil
	}
	for _, msg := range messages {
		msg.Time = time.Now().UTC()
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if err := repo.Append(ctx, ns, "messages", raw); err != nil {
			return err
		}
	}
	e.emitJSON(ctx, core.EventMemoryWrite, runID, map[string]any{"agent": agent.Name, "memory": agent.Memory, "messages": len(messages)})
	return nil
}

func (e *Engine) memoryRepository(runID string, agent core.Agent) (memory.Repository, memory.Namespace, bool) {
	if agent.Memory == "" || e.memory == nil {
		return nil, memory.Namespace{}, false
	}
	repo, ok := e.memory[agent.Memory]
	if !ok || repo == nil {
		return nil, memory.Namespace{}, false
	}
	ref, ok := e.scenario.Memories[agent.Memory]
	if !ok {
		return nil, memory.Namespace{}, false
	}
	scope := memory.Scope(ref.Scope)
	ns := memory.Namespace{Agent: agent.Name, Scope: scope}
	switch scope {
	case memory.ScopeConversation, memory.ScopeAudit:
		ns.RunID = runID
	default:
		ns.SessionID = firstNonEmpty(ref.Namespace, e.scenario.Name)
	}
	return repo, ns, true
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
	_ = e.events.Emit(ctx, core.Event{
		Type:         typ,
		RunID:        runID,
		ScenarioName: e.scenario.Name,
		Timestamp:    time.Now().UTC(),
		Payload:      payload,
	})
}

func (e *Engine) emitJSON(ctx context.Context, typ core.EventType, runID string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	e.emit(ctx, typ, runID, raw)
}
