package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aijustin/agentflow-go/internal/application/emit"
	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/internal/toolinvoke"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/retry"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

var workflowEmitWarnGate atomic.Bool

func warnWorkflowEmitFailure(ctx context.Context, runID string, err error) {
	if err == nil {
		return
	}
	if !workflowEmitWarnGate.CompareAndSwap(false, true) {
		return
	}
	defer workflowEmitWarnGate.Store(false)
	slog.WarnContext(ctx, "orchestration: event emit failed", "run_id", runID, "error", err)
}

type ToolRegistry interface {
	ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error)
}

type AgentRegistry interface {
	Agent(name string) (core.AgentRunner, bool)
}

// ConversationMemoryRewinder truncates a run-scoped conversation memory to the
// first keep messages. Workflow time-travel uses it to rewind agent memory in
// step with rewound step outputs.
type ConversationMemoryRewinder interface {
	RewindConversationMemory(ctx context.Context, runID, agentName string, keep int) error
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

func WithToolApprovalEvaluator(evaluator core.ToolApprovalEvaluator) RunnerOption {
	return func(r *WorkflowRunner) {
		r.approvalEvaluator = evaluator
	}
}

func WithBlobStore(blobs runstate.BlobStore) RunnerOption {
	return func(r *WorkflowRunner) {
		r.blobs = blobs
	}
}

// WithWorkflowToolPolicy wires a per-invocation governance policy applied to
// NodeTool executions inside a workflow.  The policy is evaluated before each
// tool call and can deny the invocation.
func WithWorkflowToolPolicy(policy governance.ToolPolicy) RunnerOption {
	return func(r *WorkflowRunner) {
		r.toolGov = policy
	}
}

// WithSecurityPolicy wires the same authorization policy used by the
// autonomous tool loop, so NodeTool executions cannot bypass RBAC.
func WithSecurityPolicy(policy security.Policy) RunnerOption {
	return func(r *WorkflowRunner) {
		r.policy = policy
	}
}

// WithAuditSink wires the audit sink used when a security policy denies a
// workflow tool invocation.
func WithAuditSink(sink audit.Sink) RunnerOption {
	return func(r *WorkflowRunner) {
		r.audit = sink
	}
}

// WithOutputRedactor wires an output redactor for persisted workflow step outputs.
func WithOutputRedactor(redactor governance.OutputRedactor) RunnerOption {
	return func(r *WorkflowRunner) {
		r.redactor = redactor
	}
}

// WithMemoryRewinder wires the capability used to rewind run-scoped
// conversation memory when a workflow is rewound via time-travel.
func WithMemoryRewinder(memory ConversationMemoryRewinder) RunnerOption {
	return func(r *WorkflowRunner) {
		r.memory = memory
	}
}

type WorkflowRunner struct {
	tools             ToolRegistry
	agents            AgentRegistry
	gate              core.HumanGate
	approvalEvaluator core.ToolApprovalEvaluator
	runs              runstate.Repository
	blobs             runstate.BlobStore
	events            core.EventSink
	policy            security.Policy
	audit             audit.Sink
	toolGov           governance.ToolPolicy
	redactor          governance.OutputRedactor
	memory            ConversationMemoryRewinder
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
	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
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
		if node, ok := workflowNodeByID(scenario, snapshot.CurrentNodeID); ok && snapshot.PendingGate == nil {
			if node.Kind == core.NodeHumanGate || node.Interrupt {
				done[snapshot.CurrentNodeID] = true
			}
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
	bodyOnly, err := loopBodyNodeIDs(workflow)
	if err != nil {
		return err
	}
	for id := range nodes {
		if bodyOnly[id] {
			continue
		}
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
		runnable := make([]string, 0, len(ready))
		for _, id := range ready {
			skip, err := r.nodeShouldSkip(ctx, runID, workflow, id)
			if err != nil {
				return err
			}
			delete(pending, id)
			if skip {
				done[id] = true
				r.emitJSON(ctx, core.EventStepCompleted, scenario.Name, runID, map[string]any{"node_id": id, "skipped": true, "reason": "edge_condition"})
				continue
			}
			runnable = append(runnable, id)
		}
		if len(runnable) == 0 {
			continue
		}
		if len(runnable) > maxParallel {
			for _, id := range runnable[maxParallel:] {
				pending[id] = true
			}
			runnable = runnable[:maxParallel]
		}
		if err := r.runBatch(ctx, scenario, runID, nodes, runnable); err != nil {
			return err
		}
		for _, id := range runnable {
			done[id] = true
		}
	}
	return nil
}

func (r *WorkflowRunner) runBatch(ctx context.Context, scenario core.Scenario, runID string, nodes map[string]core.WorkflowNode, ids []string) error {
	// A cancelable batch context lets a pause stop sibling nodes that have not
	// started yet, so we never run side effects after the run is logically
	// paused. Hard failures keep their existing semantics (siblings run to
	// completion); only a pause cancels the batch.
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for _, id := range ids {
		node := nodes[id]
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Every node in this batch runs concurrently with its
			// siblings, so none of them may update snapshot.CurrentNodeID
			// via saveStepOutput: a sibling that finishes after another
			// node in the batch has already paused (see the pause/cancel
			// handling below) would otherwise overwrite the paused node's
			// CurrentNodeID and break resume's "already done" detection.
			// safecall.Do converts a panic in user-injected dependencies
			// (event sink, repository, registry) into a node error so the
			// run settles as Failed instead of crashing the process.
			err := safecall.Do("orchestration: workflow node "+node.ID, func() error {
				nodeCtx := withSkipCurrentNode(batchCtx)
				if nodeCtx.Err() != nil {
					return nil
				}
				enabled, err := r.conditionEnabled(nodeCtx, runID, node.Condition)
				if err != nil {
					r.emitJSON(ctx, core.EventStepFailed, scenario.Name, runID, map[string]any{"node_id": node.ID, "error": err.Error()})
					return err
				}
				if !enabled {
					r.emitJSON(ctx, core.EventStepCompleted, scenario.Name, runID, map[string]any{"node_id": node.ID, "skipped": true})
					return nil
				}
				r.emitJSON(ctx, core.EventStepStarted, scenario.Name, runID, map[string]any{"node_id": node.ID})
				if err := r.runNodeWithRetry(nodeCtx, scenario, node, runID); err != nil {
					var paused WorkflowPausedError
					if errors.As(err, &paused) {
						cancel()
						return err
					}
					r.emitJSON(ctx, core.EventStepFailed, scenario.Name, runID, map[string]any{"node_id": node.ID, "error": err.Error()})
					return err
				}
				r.emitJSON(ctx, core.EventStepCompleted, scenario.Name, runID, map[string]any{"node_id": node.ID})
				return nil
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	// Prefer a pause error over any other so HITL halts propagate correctly,
	// but never silently discard a sibling's hard failure: record it as an
	// event so operators can see it even though the pause takes precedence
	// for control flow.
	var firstErr, pauseErr error
	for err := range errs {
		if err == nil {
			continue
		}
		var paused WorkflowPausedError
		if errors.As(err, &paused) {
			if pauseErr == nil {
				pauseErr = err
			}
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if pauseErr != nil {
		if firstErr != nil {
			r.emitJSON(ctx, core.EventStepFailed, scenario.Name, runID, map[string]any{
				"warning": "sibling node failed in the same batch as a pause; pause takes precedence",
				"error":   firstErr.Error(),
			})
		}
		return pauseErr
	}
	return firstErr
}

func (r *WorkflowRunner) runNodeWithRetry(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if node.Kind == core.NodeHumanGate {
		return r.runNode(ctx, scenario, node, runID)
	}
	attempts := firstPositive(node.Retry.MaxAttempts, scenario.Runtime.MaxRetries+1, 1)
	if node.Retry.MaxAttempts <= 0 && !nodeAutoRetrySafe(scenario, node) {
		// Without an explicit per-node retry policy, a tool node whose side
		// effect is write/external/dangerous is never re-executed
		// automatically: the failed attempt may already have committed its
		// side effect, and re-running it would duplicate that effect.
		attempts = 1
	}
	var lastErr error
	attempted := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		attempted = attempt
		// One idempotency key per logical node execution attempt:
		// {run_id}:{node_id}:{attempt}. A recovery replay (RetryFailedRun,
		// ResumeFromStep) re-enters this loop at attempt 1, so a replayed
		// execution reuses the same key and side-effecting tools can dedupe;
		// node-level retries are distinct executions and get distinct keys.
		// Nested executions (loop/map/subgraph children, agent-node tool
		// calls on the runtime path) overwrite the key with their own.
		attemptCtx := core.WithIdempotencyKey(ctx, workflowNodeIdempotencyKey(ctx, runID, node.ID, attempt))
		err := r.runNode(attemptCtx, scenario, node, runID)
		if err == nil {
			if node.Interrupt {
				return r.pauseForInterrupt(ctx, scenario, node, runID)
			}
			return nil
		}
		var paused WorkflowPausedError
		if errors.As(err, &paused) {
			return err
		}
		lastErr = err
		// Align with the runtime's retry classification: only errors that
		// explicitly classify themselves as retryable are re-attempted;
		// anything else is permanent and re-running would just repeat the
		// failure (or its side effects).
		if attempt == attempts || !retry.Retryable(ctx, err) {
			break
		}
		if err := retry.Backoff(ctx, attempt); err != nil {
			return err
		}
	}
	return fmt.Errorf("orchestration: node %q failed after %d attempt(s): %w", node.ID, attempted, lastErr)
}

// nodeAutoRetrySafe reports whether a node may be retried without an
// explicit per-node retry policy. Only tool nodes carry a side-effect
// classification; tools declared write/external/dangerous are excluded from
// the scenario-level automatic retry fallback.
func nodeAutoRetrySafe(scenario core.Scenario, node core.WorkflowNode) bool {
	if node.Kind != core.NodeTool {
		return true
	}
	tool, ok := scenario.Tools[node.Ref]
	if !ok {
		return true
	}
	return toolinvoke.AutoRetrySafe(tool)
}

// workflowNodeIdempotencyKey composes the idempotency key for one workflow
// node execution attempt: {run_id}:{storage_node_id}:{attempt}. The storage
// node ID includes any loop/subgraph step prefix from ctx, so a loop body
// node's key is scoped to its iteration (e.g. "run-1:loop.2.body:1").
func workflowNodeIdempotencyKey(ctx context.Context, runID, nodeID string, attempt int) string {
	return runID + ":" + storageNodeID(ctx, nodeID) + ":" + strconv.Itoa(attempt)
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
		return r.runHumanGateNode(ctx, scenario, node, runID)
	case core.NodeParallelGroup:
		return r.runParallelGroupNode(ctx, scenario, node, runID)
	case core.NodeLoop:
		return r.runLoopNode(ctx, scenario, node, runID)
	case core.NodeQueryRouter:
		return r.runQueryRouterNode(ctx, scenario, node, runID)
	case core.NodeRAGGrade:
		return r.runRAGGradeNode(ctx, scenario, node, runID)
	case core.NodeSupervisor:
		return r.runSupervisorNode(ctx, scenario, node, runID)
	case core.NodeSubgraph:
		return r.runSubgraphNode(ctx, scenario, node, runID)
	case core.NodeMap:
		return r.runMapNode(ctx, scenario, node, runID)
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
	input, err := r.resolveNodeInput(ctx, runID, node.Input, scenario.Runtime.Secrets)
	if err != nil {
		return err
	}
	ctx = core.ContextWithWorkflowNode(ctx, storageNodeID(ctx, node.ID))
	// Approval handling mirrors the autonomous runtime (maybePauseToolCall +
	// dispatchToolWithOptions): when a human gate is configured, any policy
	// that requires approval (pause / always / risky-with-side-effect)
	// pauses for a human decision rather than failing the node. Only when no
	// gate is available do we fall back to a hard denial.
	approvedResume := false
	call := llm.ToolCall{Name: node.Ref, Input: input}
	// full_trust skips static approval policies, but still honors a dynamic
	// ToolApprovalEvaluator (MCP auth, mandatory user-input tools).
	var pauseRequired bool
	if core.TrustModeFromContext(ctx) == core.TrustModeFullTrust {
		if r.approvalEvaluator != nil {
			pauseRequired, err = r.approvalEvaluator.PauseRequired(ctx, runID, tool, call)
			if err != nil {
				return err
			}
		}
		if !pauseRequired {
			approvedResume = true
		}
	} else {
		pauseRequired, err = toolinvoke.EvaluatePauseRequired(ctx, tool, r.approvalEvaluator, runID, call)
		if err != nil {
			return err
		}
	}
	if pauseRequired {
		if r.gate == nil {
			if reason := toolinvoke.DenialWithoutGate(tool, false, false); reason != "" {
				return fmt.Errorf("orchestration: tool %q: %s", node.Ref, reason)
			}
		}
		if approvedInput, ok, err := r.workflowToolApprovalInput(ctx, runID, node.ID); err != nil {
			return err
		} else if ok {
			input = approvedInput
			call.Input = approvedInput
			approvedResume = true
			if err := r.clearWorkflowToolApprovalCheckpoint(ctx, runID); err != nil {
				return err
			}
		} else if r.gate != nil {
			return r.pauseForWorkflowToolApproval(ctx, scenario, node, runID, tool, input)
		}
	}
	if reason := toolinvoke.DenialWithoutGateWithEvaluator(ctx, tool, r.gate != nil, approvedResume, r.approvalEvaluator, runID, call); reason != "" {
		return fmt.Errorf("orchestration: tool %q: %s", node.Ref, reason)
	}
	validateToolInput := scenario.Runtime.ValidateToolInput || !scenario.Runtime.DisableToolInputValidation
	if err := toolinvoke.ValidateInput(validateToolInput, tool, input); err != nil {
		return fmt.Errorf("orchestration: tool %q: %w", node.Ref, err)
	}
	rateCapReserved := false
	if tool.RateCap > 0 {
		// Check-and-reserve is one atomic CAS pass: parallel sibling nodes
		// cannot both observe count < RateCap and both execute.
		if err := r.reserveWorkflowToolCall(ctx, runID, node.Ref, tool.RateCap); err != nil {
			return err
		}
		rateCapReserved = true
		// Any exit before a successful execution returns the reservation so
		// the cap keeps counting successful executions only.
		defer func() {
			if rateCapReserved {
				r.releaseWorkflowToolCall(ctx, runID, node.Ref)
			}
		}()
	}
	resource := toolinvoke.SecurityResource(node.Ref, tool, map[string]string{"node_id": node.ID})
	if err := r.authorizeTool(ctx, runID, resource); err != nil {
		return fmt.Errorf("orchestration: tool %q unauthorized: %w", node.Ref, err)
	}
	if r.toolGov != nil {
		if err := r.toolGov.AuthorizeTool(ctx, governance.ToolInvocation{
			RunID:      runID,
			Tool:       node.Ref,
			SideEffect: tool.SideEffect,
			Input:      input,
		}); err != nil {
			return fmt.Errorf("orchestration: tool %q denied by governance: %w", node.Ref, err)
		}
	}
	executor, ok, err := r.tools.ResolveTool(ctx, tool)
	if err != nil {
		return fmt.Errorf("orchestration: resolve tool %q: %w", node.Ref, err)
	}
	if !ok {
		return fmt.Errorf("orchestration: tool %q not found", node.Ref)
	}
	// Node-level retry (including the side-effect safety gate) is applied by
	// runNodeWithRetry, so a single execution here is correct. A per-tool
	// Timeout mirrors the autonomous executeToolWithRetry bound.
	execCtx, cancelTimeout := workflowTimeout(ctx, tool.Timeout)
	defer cancelTimeout()
	result, err := safecall.Invoke("orchestration: tool execute", func() (core.ToolResult, error) {
		return executor.Execute(execCtx, core.ToolCall{RunID: runID, Tool: node.Ref, Input: input})
	})
	if err != nil {
		return err
	}
	// A tool that signals failure via ToolResult.Error (with a nil Go error)
	// must fail the workflow node instead of being persisted as a successful
	// step output.
	if result.Error != "" {
		return fmt.Errorf("orchestration: tool %q failed: %s", node.Ref, result.Error)
	}
	// The execution succeeded: the rate-cap reservation becomes the recorded
	// successful execution, so the deferred release must not run. This holds
	// even if the step-output save below fails, matching the historical
	// increment-then-persist ordering.
	rateCapReserved = false
	return r.saveStepOutput(ctx, scenario, runID, node.ID, result)
}

func (r *WorkflowRunner) runAgentNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if r.agents == nil {
		return fmt.Errorf("orchestration: agent registry is required")
	}
	agent, ok := r.agents.Agent(node.Ref)
	if !ok {
		return fmt.Errorf("orchestration: agent %q not found", node.Ref)
	}
	input, err := r.resolveNodeInput(ctx, runID, node.Input, scenario.Runtime.Secrets)
	if err != nil {
		return err
	}
	agentInput := coreAgentInputFromResolved(input)
	agentInput.RunID = runID
	agentInput, amendmentApplied, err := r.resolveWorkflowAmendmentForAgent(ctx, runID, agentInput)
	if err != nil {
		return err
	}
	if amendmentApplied {
		if err := r.clearWorkflowAmendment(ctx, runID); err != nil {
			return err
		}
	}
	ctx = core.ContextWithWorkflowNode(ctx, storageNodeID(ctx, node.ID))
	output, err := safecall.Invoke("orchestration: agent run", func() (core.AgentOutput, error) {
		return agent.Run(ctx, agentInput)
	})
	if err != nil {
		return err
	}
	return r.saveStepOutput(ctx, scenario, runID, node.ID, output)
}

func (r *WorkflowRunner) runHumanGateNode(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if r.gate == nil {
		return fmt.Errorf("orchestration: human gate is required")
	}
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for human gate")
	}
	// CurrentNodeID is stored with its full storage-scoped id (including any
	// subgraph/loop prefix) so that resuming from inside a subgraph or loop
	// can recognize that this exact gate was already approved, instead of
	// re-pausing on it a second time.
	storedID := storageNodeID(ctx, node.ID)
	prepare := func(snapshot *runstate.RunSnapshot) error {
		snapshot.CurrentNodeID = storedID
		return nil
	}
	token, err := r.pauseWithRetry(ctx, runID, prepare, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: runID, Version: version, NodeID: node.ID, Payload: node.Input}
	})
	if err != nil {
		return err
	}
	r.emitJSON(ctx, core.EventRunPaused, scenario.Name, runID, map[string]any{"node_id": node.ID})
	return WorkflowPausedError{RunID: runID, NodeID: node.ID, Token: token}
}

func (r *WorkflowRunner) pauseForInterrupt(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string) error {
	if r.gate == nil {
		return fmt.Errorf("orchestration: human gate is required for interrupt on node %q", node.ID)
	}
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for interrupt")
	}
	storedID := storageNodeID(ctx, node.ID)
	var payload json.RawMessage
	prepare := func(snapshot *runstate.RunSnapshot) error {
		payloadMap := map[string]any{"node_id": node.ID, "interrupt": true}
		if ref, ok := snapshot.StepOutputs[storedID]; ok {
			payloadMap["output"] = ref
		}
		raw, err := json.Marshal(payloadMap)
		if err != nil {
			return fmt.Errorf("orchestration: interrupt payload for node %q: %w", node.ID, err)
		}
		payload = raw
		snapshot.CurrentNodeID = storedID
		return nil
	}
	token, err := r.pauseWithRetry(ctx, runID, prepare, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: runID, Version: version, NodeID: node.ID, Payload: payload}
	})
	if err != nil {
		return err
	}
	r.emitJSON(ctx, core.EventRunPaused, scenario.Name, runID, map[string]any{"node_id": node.ID, "interrupt": true})
	return WorkflowPausedError{RunID: runID, NodeID: node.ID, Token: token}
}

// saveSnapshotWithRetry reloads and mutates the run snapshot, retrying on
// optimistic-concurrency conflicts so concurrent writers (parallel batch
// siblings, tool loops) never turn a legitimate write into a hard failure.
func (r *WorkflowRunner) saveSnapshotWithRetry(ctx context.Context, runID string, mutate func(*runstate.RunSnapshot) error) error {
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return err
		}
		if mutate != nil {
			if err := mutate(&snapshot); err != nil {
				return err
			}
		}
		if err := r.saveRunSnapshot(ctx, &snapshot, snapshot.Version); err != nil {
			// ErrStaleFence passes straight through: a newer lease holder
			// owns the run, so retrying can never succeed.
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("orchestration: failed to save snapshot %q after stale snapshot retries", runID)
}

// saveRunSnapshot persists a run snapshot with lease fencing when the
// context carries a fence token and the repository supports it (see
// runstate.SaveWithFence). The fallback warning for fencing-incapable
// repositories is emitted once per process by the framework facade / engine,
// which always save before the runner does.
func (r *WorkflowRunner) saveRunSnapshot(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	_, err := runstate.SaveWithFence(ctx, r.runs, snapshot, expectedVersion)
	return err
}

// pauseWithRetry applies an optional snapshot mutation and then pauses
// through the human gate, retrying the whole sequence when a concurrent
// writer (e.g. a sibling node in the same parallel batch) advances the run
// version between our load and the gate's own compare-and-swap save. Without
// this retry, HumanGate.Pause implementations that use a single fixed
// expected version turn a legitimate concurrent pause into a hard run
// failure.
func (r *WorkflowRunner) pauseWithRetry(ctx context.Context, runID string, prepare func(*runstate.RunSnapshot) error, build func(version int64) core.CheckpointState) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if prepare != nil {
			if err := r.saveSnapshotWithRetry(ctx, runID, prepare); err != nil {
				return "", err
			}
		}
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return "", err
		}
		token, err := r.gate.Pause(ctx, build(snapshot.Version))
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, runstate.ErrStaleSnapshot) {
			return "", err
		}
	}
	return "", fmt.Errorf("orchestration: failed to pause run %q after stale snapshot retries", runID)
}

// nodeAlreadyDone reports whether n's storage-scoped output already exists,
// or - for human-gate/interrupt nodes that never write a step output - that
// the run's CurrentNodeID shows this exact node was already paused and then
// resolved (PendingGate cleared). Loop and subgraph resume paths use this to
// skip nodes that already ran without re-triggering their side effects or
// re-prompting for approval.
func (r *WorkflowRunner) nodeAlreadyDone(ctx context.Context, runID string, n core.WorkflowNode) (bool, error) {
	if _, ok, err := r.stepOutputRaw(ctx, runID, n.ID); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	if n.Kind != core.NodeHumanGate && !n.Interrupt {
		return false, nil
	}
	if r.runs == nil {
		return false, nil
	}
	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return false, err
	}
	return snapshot.PendingGate == nil && snapshot.CurrentNodeID == storageNodeID(ctx, n.ID), nil
}

func (r *WorkflowRunner) saveStepOutput(ctx context.Context, scenario core.Scenario, runID, nodeID string, value any) error {
	return r.SaveStepOutput(ctx, scenario, runID, nodeID, value)
}

// SaveStepOutput persists a workflow node output and advances CurrentNodeID.
func (r *WorkflowRunner) SaveStepOutput(ctx context.Context, scenario core.Scenario, runID, nodeID string, value any) error {
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required to save step output")
	}
	nodeID = storageNodeID(ctx, nodeID)
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
		if err != nil {
			return err
		}
		if snapshot.StepOutputs == nil {
			snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
		}
		if !skipCurrentNodeUpdate(ctx) {
			snapshot.CurrentNodeID = nodeID
		}
		ref, err := r.stepOutputRef(ctx, runID, nodeID, scenario.Runtime.StepOutputThreshold, raw)
		if err != nil {
			return err
		}
		snapshot.StepOutputs[nodeID] = ref
		err = r.saveRunSnapshot(ctx, &snapshot, snapshot.Version)
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
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
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
		if err != nil {
			return false, err
		}
		if !found {
			return true, nil
		}
		expected := parseConditionValue(strings.TrimSpace(args[1]))
		return !workflowValuesEqual(actual, expected), nil
	}
	return false, fmt.Errorf("orchestration: unsupported condition %q", condition)
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
	storageID := storageNodeID(ctx, nodeID)
	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return nil, false, err
	}
	ref, ok := snapshot.StepOutputs[storageID]
	if !ok && storageID != nodeID {
		ref, ok = snapshot.StepOutputs[nodeID]
	}
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
	leftNorm := normalizeWorkflowValue(left)
	rightNorm := normalizeWorkflowValue(right)
	leftBytes, leftErr := json.Marshal(leftNorm)
	rightBytes, rightErr := json.Marshal(rightNorm)
	if leftErr == nil && rightErr == nil {
		return string(leftBytes) == string(rightBytes)
	}
	return fmt.Sprint(leftNorm) == fmt.Sprint(rightNorm)
}

func normalizeWorkflowValue(value any) any {
	switch typed := value.(type) {
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed)
		}
		return typed
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return i
		}
		if f, err := typed.Float64(); err == nil {
			return normalizeWorkflowValue(f)
		}
		return typed.String()
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeWorkflowValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeWorkflowValue(item)
		}
		return out
	default:
		return value
	}
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

func (r *WorkflowRunner) stepOutputRef(ctx context.Context, runID, nodeID string, threshold int64, raw json.RawMessage) (runstate.StepOutputRef, error) {
	if r.redactor != nil {
		redacted, err := r.redactor.RedactOutput(ctx, governance.OutputRedaction{
			RunID:  runID,
			StepID: nodeID,
			Kind:   "workflow_step_output",
			Data:   raw,
		})
		if err != nil {
			return runstate.StepOutputRef{}, err
		}
		raw = redacted
	}
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
	return nodeByID(*scenario.Orchestration.Workflow, id)
}

func nodeByID(workflow core.Workflow, id string) (core.WorkflowNode, bool) {
	for _, node := range workflow.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return core.WorkflowNode{}, false
}

// jsonStringValue encodes s as a JSON string value; unlike fmt.Sprintf("%q")
// it never emits Go-only escapes (\xNN) that are invalid JSON.
func jsonStringValue(s string) json.RawMessage {
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return raw
}

func (r *WorkflowRunner) emitJSON(ctx context.Context, typ core.EventType, scenarioName, runID string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		fallback, fallbackErr := json.Marshal(map[string]string{"error": err.Error()})
		if fallbackErr != nil {
			fallback = []byte(`{"error":"marshal failed"}`)
		}
		raw = fallback
	}
	r.emit(ctx, typ, scenarioName, runID, raw)
}

func (r *WorkflowRunner) emit(ctx context.Context, typ core.EventType, scenarioName, runID string, payload json.RawMessage) {
	event := emit.BuildEvent(ctx, scenarioName, r.redactor, typ, runID, payload)
	if err := r.events.Emit(ctx, event); err != nil {
		// WorkflowRunner has no logger; avoid silent total loss by recording
		// once via the standard library when emit fails.
		warnWorkflowEmitFailure(ctx, runID, err)
	}
}

// authorizeTool mirrors the autonomous runtime's security.Policy check so a
// workflow NodeTool cannot bypass RBAC that would deny the same tool in a
// tool loop. When no policy is configured this is a no-op.
func (r *WorkflowRunner) authorizeTool(ctx context.Context, runID string, resource security.Resource) error {
	if r.policy == nil {
		return nil
	}
	principal, err := identity.RequirePrincipal(ctx)
	if err != nil {
		r.recordAudit(ctx, audit.Event{
			Type:      audit.EventPolicyDenied,
			Principal: identity.Principal{},
			Action:    security.ActionToolInvoke,
			Resource:  resource,
			RunID:     runID,
			Outcome:   "denied",
			Reason:    security.ErrUnauthenticated.Error(),
		})
		return security.ErrUnauthenticated
	}
	if err := r.policy.Authorize(ctx, principal, security.ActionToolInvoke, resource); err != nil {
		r.recordAudit(ctx, audit.Event{
			Type:      audit.EventPolicyDenied,
			Principal: principal,
			Action:    security.ActionToolInvoke,
			Resource:  resource,
			RunID:     runID,
			Outcome:   "denied",
			Reason:    err.Error(),
		})
		return err
	}
	return nil
}

func (r *WorkflowRunner) recordAudit(ctx context.Context, event audit.Event) {
	if r.audit == nil {
		return
	}
	_ = r.audit.Record(ctx, event.WithDefaults(time.Now().UTC()))
}
