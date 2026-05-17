package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type ToolRegistry interface {
	ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error)
}

type AgentRegistry interface {
	Agent(name string) (core.AgentRunner, bool)
}

type RunnerOption func(*WorkflowRunner)

func WithAgentRegistry(agents AgentRegistry) RunnerOption {
	return func(r *WorkflowRunner) {
		r.agents = agents
	}
}

func WithHumanGate(gate core.HumanGate) RunnerOption {
	return func(r *WorkflowRunner) {
		r.gate = gate
	}
}

func WithBlobStore(blobs runstate.BlobStore) RunnerOption {
	return func(r *WorkflowRunner) {
		r.blobs = blobs
	}
}

type WorkflowRunner struct {
	tools  ToolRegistry
	agents AgentRegistry
	gate   core.HumanGate
	runs   runstate.Repository
	blobs  runstate.BlobStore
	events core.EventSink
}

type WorkflowPausedError struct {
	RunID  string
	NodeID string
	Token  string
}

func (e WorkflowPausedError) Error() string {
	return fmt.Sprintf("orchestration: workflow paused at node %q", e.NodeID)
}

func NewWorkflowRunner(tools ToolRegistry, runs runstate.Repository, events core.EventSink, opts ...RunnerOption) *WorkflowRunner {
	if events == nil {
		events = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	}
	runner := &WorkflowRunner{tools: tools, runs: runs, events: events}
	for _, opt := range opts {
		if opt != nil {
			opt(runner)
		}
	}
	return runner
}

func (r *WorkflowRunner) Run(ctx context.Context, scenario core.Scenario, runID string) error {
	ctx, cancel := workflowTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	return r.run(ctx, scenario, runID, nil)
}

func (r *WorkflowRunner) Resume(ctx context.Context, scenario core.Scenario, runID string) error {
	ctx, cancel := workflowTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for workflow resume")
	}
	snapshot, err := r.runs.Load(ctx, runID)
	if err != nil {
		return err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return fmt.Errorf("orchestration: workflow resume requires running snapshot, got %s", snapshot.Status)
	}
	done := make(map[string]bool, len(snapshot.StepOutputs)+1)
	for nodeID := range snapshot.StepOutputs {
		done[nodeID] = true
	}
	if snapshot.CurrentNodeID != "" {
		if node, ok := workflowNodeByID(scenario, snapshot.CurrentNodeID); ok && node.Kind == core.NodeHumanGate && snapshot.PendingGate == nil {
			done[snapshot.CurrentNodeID] = true
		}
	}
	return r.run(ctx, scenario, runID, done)
}

func (r *WorkflowRunner) run(ctx context.Context, scenario core.Scenario, runID string, alreadyDone map[string]bool) error {
	if scenario.Orchestration.Workflow == nil {
		return fmt.Errorf("orchestration: workflow is required")
	}
	workflow := *scenario.Orchestration.Workflow
	nodes := make(map[string]core.WorkflowNode, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		nodes[node.ID] = node
	}
	deps := dependencies(workflow)
	pending := make(map[string]bool, len(nodes))
	done := make(map[string]bool, len(nodes))
	for id := range nodes {
		if alreadyDone[id] {
			done[id] = true
			continue
		}
		pending[id] = true
	}
	maxParallel := firstPositive(scenario.Orchestration.MaxParallel, scenario.Runtime.MaxParallel, 1)
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready := readyNodes(pending, done, deps)
		if len(ready) == 0 {
			return fmt.Errorf("orchestration: workflow has no ready nodes; remaining=%v", mapKeys(pending))
		}
		slices.Sort(ready)
		if len(ready) > maxParallel {
			ready = ready[:maxParallel]
		}
		if err := r.runBatch(ctx, scenario, runID, nodes, ready); err != nil {
			return err
		}
		for _, id := range ready {
			delete(pending, id)
			done[id] = true
		}
	}
	return nil
}

func (r *WorkflowRunner) runBatch(ctx context.Context, scenario core.Scenario, runID string, nodes map[string]core.WorkflowNode, ids []string) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for _, id := range ids {
		node := nodes[id]
		wg.Add(1)
		go func() {
			defer wg.Done()
			enabled, err := r.conditionEnabled(ctx, runID, node.Condition)
			if err != nil {
				r.emitJSON(ctx, core.EventStepFailed, scenario.Name, runID, map[string]any{"node_id": node.ID, "error": err.Error()})
				errs <- err
				return
			}
			if !enabled {
				r.emitJSON(ctx, core.EventStepCompleted, scenario.Name, runID, map[string]any{"node_id": node.ID, "skipped": true})
				return
			}
			r.emitJSON(ctx, core.EventStepStarted, scenario.Name, runID, map[string]any{"node_id": node.ID})
			if err := r.runNodeWithRetry(ctx, scenario, node, runID); err != nil {
				var paused WorkflowPausedError
				if errors.As(err, &paused) {
					errs <- err
					return
				}
				r.emitJSON(ctx, core.EventStepFailed, scenario.Name, runID, map[string]any{"node_id": node.ID, "error": err.Error()})
				errs <- err
				return
			}
			r.emitJSON(ctx, core.EventStepCompleted, scenario.Name, runID, map[string]any{"node_id": node.ID})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func (r *WorkflowRunner) runNodeWithRetry(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if node.Kind == core.NodeHumanGate {
		return r.runNode(ctx, scenario, node, runID)
	}
	attempts := firstPositive(node.Retry.MaxAttempts, scenario.Runtime.MaxRetries+1, 1)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.runNode(ctx, scenario, node, runID); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("orchestration: node %q failed after %d attempt(s): %w", node.ID, attempts, lastErr)
}

func (r *WorkflowRunner) runNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	switch node.Kind {
	case core.NodeTool:
		return r.runToolNode(ctx, scenario, node, runID)
	case core.NodeAgent:
		return r.runAgentNode(ctx, scenario, node, runID)
	case core.NodeTransform:
		return r.runTransformNode(ctx, scenario, node, runID)
	case core.NodeHumanGate:
		return r.runHumanGateNode(ctx, node, runID)
	case core.NodeSkill:
		return fmt.Errorf("orchestration: skill node %q requires skill workflow expansion before runtime", node.ID)
	default:
		return fmt.Errorf("orchestration: unsupported node kind %q", node.Kind)
	}
}

type transformSpec struct {
	Set  map[string]any    `json:"set"`
	Copy map[string]string `json:"copy"`
}

func (r *WorkflowRunner) runTransformNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if len(node.Input) == 0 {
		return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]json.RawMessage{"input": node.Input})
	}
	var spec transformSpec
	if err := json.Unmarshal(node.Input, &spec); err != nil {
		return fmt.Errorf("orchestration: transform node %q decode input: %w", node.ID, err)
	}
	if len(spec.Set) == 0 && len(spec.Copy) == 0 {
		return r.saveStepOutput(ctx, scenario, runID, node.ID, map[string]json.RawMessage{"input": node.Input})
	}
	output := cloneAnyMap(spec.Set)
	for field, path := range spec.Copy {
		value, ok, err := r.resolveWorkflowPath(ctx, runID, path)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("orchestration: transform node %q path %q not found", node.ID, path)
		}
		output[field] = value
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, output)
}

func (r *WorkflowRunner) runToolNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if r.tools == nil {
		return fmt.Errorf("orchestration: tool registry is required")
	}
	tool := scenario.Tools[node.Ref]
	if tool.Name == "" {
		tool.Name = node.Ref
	}
	executor, ok, err := r.tools.ResolveTool(ctx, tool)
	if err != nil {
		return fmt.Errorf("orchestration: resolve tool %q: %w", node.Ref, err)
	}
	if !ok {
		return fmt.Errorf("orchestration: tool %q not found", node.Ref)
	}
	result, err := executor.Execute(ctx, core.ToolCall{RunID: runID, Tool: node.Ref, Input: node.Input})
	if err != nil {
		return err
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, result)
}

func (r *WorkflowRunner) runAgentNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if r.agents == nil {
		if agent, ok := scenario.Agents[node.Ref]; ok {
			return r.saveStepOutput(ctx, scenario, runID, node.ID, core.AgentOutput{RunID: runID, Text: fmt.Sprintf("agent %s completed", agent.Name), Raw: node.Input})
		}
		return fmt.Errorf("orchestration: agent registry is required")
	}
	agent, ok := r.agents.Agent(node.Ref)
	if !ok {
		return fmt.Errorf("orchestration: agent %q not found", node.Ref)
	}
	output, err := agent.Run(ctx, core.AgentInput{RunID: runID, Context: node.Input})
	if err != nil {
		return err
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, output)
}

func (r *WorkflowRunner) runHumanGateNode(ctx context.Context, node core.WorkflowNode, runID string) error {
	if r.gate == nil {
		return fmt.Errorf("orchestration: human gate is required")
	}
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for human gate")
	}
	snapshot, err := r.runs.Load(ctx, runID)
	if err != nil {
		return err
	}
	snapshot.CurrentNodeID = node.ID
	if err := r.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
		return err
	}
	version := snapshot.Version
	token, err := r.gate.Pause(ctx, core.CheckpointState{RunID: runID, Version: version, NodeID: node.ID, Payload: node.Input})
	if err != nil {
		return err
	}
	r.emitJSON(ctx, core.EventRunPaused, "", runID, map[string]any{"node_id": node.ID})
	return WorkflowPausedError{RunID: runID, NodeID: node.ID, Token: token}
}

func (r *WorkflowRunner) saveStepOutput(ctx context.Context, scenario core.Scenario, runID, nodeID string, value any) error {
	if r.runs == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := r.runs.Load(ctx, runID)
		if err != nil {
			return err
		}
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		snapshot.CurrentNodeID = nodeID
		ref, err := r.stepOutputRef(ctx, scenario.Runtime.StepOutputThreshold, raw)
		if err != nil {
			return err
		}
		snapshot.StepOutputs[nodeID] = ref
		err = r.runs.Save(ctx, &snapshot, snapshot.Version)
		if err == nil {
			return nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return err
		}
	}
	return fmt.Errorf("orchestration: failed to save node %q output after stale snapshot retries", nodeID)
}

func dependencies(workflow core.Workflow) map[string]map[string]bool {
	deps := make(map[string]map[string]bool, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		deps[node.ID] = make(map[string]bool)
		for _, dep := range node.DependsOn {
			deps[node.ID][dep] = true
		}
	}
	for _, edge := range workflow.Edges {
		if !staticConditionEnabled(edge.Condition) {
			continue
		}
		if deps[edge.To] == nil {
			deps[edge.To] = make(map[string]bool)
		}
		deps[edge.To][edge.From] = true
	}
	return deps
}

func readyNodes(pending, done map[string]bool, deps map[string]map[string]bool) []string {
	ready := make([]string, 0)
	for id := range pending {
		ok := true
		for dep := range deps[id] {
			if !done[dep] {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, id)
		}
	}
	return ready
}

func staticConditionEnabled(condition string) bool {
	switch strings.TrimSpace(condition) {
	case "", "true", "always":
		return true
	case "false", "never":
		return false
	default:
		return true
	}
}

func (r *WorkflowRunner) conditionEnabled(ctx context.Context, runID, condition string) (bool, error) {
	condition = strings.TrimSpace(condition)
	switch condition {
	case "", "true", "always":
		return true, nil
	case "false", "never":
		return false, nil
	}
	if inner, ok := functionCall(condition, "exists"); ok {
		_, found, err := r.resolveWorkflowPath(ctx, runID, strings.TrimSpace(inner))
		return found, err
	}
	if inner, ok := functionCall(condition, "missing"); ok {
		_, found, err := r.resolveWorkflowPath(ctx, runID, strings.TrimSpace(inner))
		return !found, err
	}
	if inner, ok := functionCall(condition, "eq"); ok {
		args := splitConditionArgs(inner)
		if len(args) != 2 {
			return false, fmt.Errorf("orchestration: eq condition requires path and expected value")
		}
		actual, found, err := r.resolveWorkflowPath(ctx, runID, strings.TrimSpace(args[0]))
		if err != nil || !found {
			return false, err
		}
		expected := parseConditionValue(strings.TrimSpace(args[1]))
		return workflowValuesEqual(actual, expected), nil
	}
	if inner, ok := functionCall(condition, "ne"); ok {
		args := splitConditionArgs(inner)
		if len(args) != 2 {
			return false, fmt.Errorf("orchestration: ne condition requires path and expected value")
		}
		actual, found, err := r.resolveWorkflowPath(ctx, runID, strings.TrimSpace(args[0]))
		if err != nil || !found {
			return false, err
		}
		expected := parseConditionValue(strings.TrimSpace(args[1]))
		return !workflowValuesEqual(actual, expected), nil
	}
	return true, nil
}

func (r *WorkflowRunner) resolveWorkflowPath(ctx context.Context, runID, path string) (any, bool, error) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) < 2 || parts[0] != "steps" {
		return nil, false, fmt.Errorf("orchestration: workflow path %q must start with steps.<node_id>", path)
	}
	raw, ok, err := r.stepOutputRaw(ctx, runID, parts[1])
	if err != nil || !ok {
		return nil, ok, err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, fmt.Errorf("orchestration: decode step output %q: %w", parts[1], err)
	}
	current := value
	for _, part := range parts[2:] {
		if part == "" {
			return nil, false, nil
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false, nil
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false, nil
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func (r *WorkflowRunner) stepOutputRaw(ctx context.Context, runID, nodeID string) (json.RawMessage, bool, error) {
	if r.runs == nil {
		return nil, false, fmt.Errorf("orchestration: run-state repository is required for workflow expressions")
	}
	snapshot, err := r.runs.Load(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	ref, ok := snapshot.StepOutputs[nodeID]
	if !ok {
		return nil, false, nil
	}
	if ref.Blob != nil {
		if r.blobs == nil {
			return nil, false, fmt.Errorf("orchestration: blob store is required for externalized step output %q", nodeID)
		}
		raw, err := r.blobs.Get(ctx, *ref.Blob)
		return raw, err == nil, err
	}
	return ref.Inline, true, nil
}

func functionCall(condition, name string) (string, bool) {
	prefix := name + "("
	if !strings.HasPrefix(condition, prefix) || !strings.HasSuffix(condition, ")") {
		return "", false
	}
	return strings.TrimSpace(condition[len(prefix) : len(condition)-1]), true
}

func splitConditionArgs(input string) []string {
	args := make([]string, 0, 2)
	var builder strings.Builder
	inString := false
	escaped := false
	for _, r := range input {
		switch {
		case escaped:
			builder.WriteRune(r)
			escaped = false
		case r == '\\' && inString:
			builder.WriteRune(r)
			escaped = true
		case r == '"':
			builder.WriteRune(r)
			inString = !inString
		case r == ',' && !inString:
			args = append(args, strings.TrimSpace(builder.String()))
			builder.Reset()
		default:
			builder.WriteRune(r)
		}
	}
	args = append(args, strings.TrimSpace(builder.String()))
	return args
}

func parseConditionValue(raw string) any {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		return value
	}
	return strings.Trim(raw, `"`)
}

func workflowValuesEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	if leftErr == nil && rightErr == nil {
		return string(leftBytes) == string(rightBytes)
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return make(map[string]any)
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func workflowTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (r *WorkflowRunner) stepOutputRef(ctx context.Context, threshold int64, raw json.RawMessage) (runstate.StepOutputRef, error) {
	if threshold <= 0 || int64(len(raw)) <= threshold || r.blobs == nil {
		return runstate.StepOutputRef{Inline: raw}, nil
	}
	ref, err := r.blobs.Put(ctx, raw)
	if err != nil {
		return runstate.StepOutputRef{}, err
	}
	return runstate.StepOutputRef{Blob: &ref}, nil
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func workflowNodeByID(scenario core.Scenario, id string) (core.WorkflowNode, bool) {
	if scenario.Orchestration.Workflow == nil {
		return core.WorkflowNode{}, false
	}
	for _, node := range scenario.Orchestration.Workflow.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return core.WorkflowNode{}, false
}

func (r *WorkflowRunner) emitJSON(ctx context.Context, typ core.EventType, scenarioName, runID string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	r.emit(ctx, typ, scenarioName, runID, raw)
}

func (r *WorkflowRunner) emit(ctx context.Context, typ core.EventType, scenarioName, runID string, payload json.RawMessage) {
	_ = r.events.Emit(ctx, core.Event{
		Type:         typ,
		RunID:        runID,
		ScenarioName: scenarioName,
		Timestamp:    time.Now().UTC(),
		Payload:      payload,
	})
}
