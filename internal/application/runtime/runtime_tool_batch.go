package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/internal/toolinvoke"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/security"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type toolBatchItem struct {
	call          llm.ToolCall
	result        core.ToolResult
	contextResult core.ToolResult
	transformMeta contextwindow.TransformMeta
	toolMsg       memoryMessage
	message       llm.Message
}

func (e *Engine) toolCallNeedsPause(ctx context.Context, runID string, call llm.ToolCall) (bool, error) {
	tool, ok := e.scenario.Tools[call.Name]
	if !ok {
		return false, nil
	}
	var pauseRequired bool
	var err error
	if TrustModeFromContext(ctx) == TrustModeFullTrust {
		if e.approvalEvaluator != nil {
			pauseRequired, err = e.approvalEvaluator.PauseRequired(ctx, runID, tool, call)
		}
	} else {
		pauseRequired, err = toolinvoke.EvaluatePauseRequired(ctx, tool, e.approvalEvaluator, runID, call)
	}
	if err != nil {
		return false, err
	}
	if !pauseRequired {
		return false, nil
	}
	// Matching maybePauseToolCall: without a gate there is nowhere to pause,
	// so the call proceeds (and may be denied later by DenialWithoutGate).
	if e.gate == nil {
		return false, nil
	}
	return true, nil
}

func (e *Engine) toolBatchConcurrency(batchLen int) int {
	limit := firstPositive(e.scenario.Orchestration.MaxParallel, e.scenario.Runtime.MaxParallel)
	if limit <= 0 {
		limit = batchLen
	}
	if limit > batchLen {
		limit = batchLen
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

func (e *Engine) executeToolBatch(
	ctx context.Context,
	runID string,
	agent core.Agent,
	profile core.LLMProfileRef,
	calls []llm.ToolCall,
	tracker *toolCallTracker,
	emit streamChunkSink,
) ([]toolBatchItem, error) {
	items := make([]toolBatchItem, len(calls))
	for i, call := range calls {
		items[i].call = call
	}
	if len(calls) == 0 {
		return items, nil
	}
	concurrency := e.toolBatchConcurrency(len(calls))
	pathLocks := newKeyedLockSet()
	governanceLocks := newKeyedLockSet()
	sem := semaphore.NewWeighted(int64(concurrency))
	group, groupCtx := errgroup.WithContext(ctx)

	for i := range calls {
		index := i
		call := calls[index]
		group.Go(func() error {
			if err := sem.Acquire(groupCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			unlock := pathLocks.acquire(lockPathForArgs(call.Input))
			defer unlock()
			// Always taken after the path lock so the two sets have one global
			// order and cannot deadlock against each other.
			unlockGovernance := governanceLocks.acquire(e.governanceLockKey(call))
			defer unlockGovernance()

			toolCtx := withToolProgressSink(groupCtx, emit)
			// skipPersist: tool I/O runs in parallel, but every result is
			// persisted below in one saveStepOutputs after group.Wait,
			// instead of N goroutines racing optimistic-CAS writes on the
			// same run snapshot.
			result, err := e.dispatchToolWithOptions(toolCtx, runID, agent, call, tracker, toolDispatchOptions{skipPersist: true})
			if err != nil {
				return err
			}
			items[index].result = result
			if result.Error != "" {
				emitStreamChunk(emit, llm.ChatChunk{
					Kind:       llm.ChunkKindToolDenied,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					ToolError:  result.Error,
					ToolOutput: result.Output,
				})
			} else {
				emitStreamChunk(emit, llm.ChatChunk{
					Kind:       llm.ChunkKindToolResult,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					ToolOutput: result.Output,
				})
			}
			return e.materializeToolBatchItem(&items[index], profile)
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	outputs := make(map[string]any, len(items))
	for i := range items {
		outputs[e.batchPersistKey(agent, items[i].call)] = items[i].result
	}
	if err := e.saveStepOutputs(ctx, runID, outputs); err != nil {
		// The tools already executed; make the persistence failure visible on
		// the affected results (and via persist_error events + audit) instead
		// of losing it, matching the inline persist-failure semantics of
		// dispatchToolWithOptions.
		for i := range items {
			if items[i].result.Error == "" {
				items[i].result.Error = "persist tool output: " + err.Error()
				if matErr := e.materializeToolBatchItem(&items[i], profile); matErr != nil {
					e.logWarn(ctx, "runtime: failed to rebuild tool message after persist failure", "run_id", runID, "tool", items[i].call.Name, "error", matErr)
				}
			} else {
				e.logWarn(ctx, "runtime: failed to persist tool output after tool error", "run_id", runID, "tool", items[i].call.Name, "error", err)
			}
			e.recordAudit(ctx, audit.Event{Type: audit.EventToolInvoked, Principal: principalFromContext(ctx), Action: security.ActionToolInvoke, Resource: toolResource(agent, items[i].call, nil), RunID: runID, Outcome: toolOutcome(items[i].result)})
			e.emitJSON(ctx, core.EventToolReturned, runID, map[string]any{"agent": agent.Name, "tool": items[i].call.Name, "tool_call_id": items[i].call.ID, "idempotency_key": toolIdempotencyKey(runID, items[i].call), "error": items[i].result.Error, "persist_error": err.Error()})
		}
	}
	return items, nil
}

// batchPersistKey returns the step-output key a dispatched call is stored
// under: delegation results live under the sub-agent namespace, everything
// else under tool.<callID>.
func (e *Engine) batchPersistKey(agent core.Agent, call llm.ToolCall) string {
	if subAgentName, ok := e.delegateTarget(agent, call.Name); ok {
		return "agent." + subAgentName + "." + call.ID
	}
	return "tool." + call.ID
}

// materializeToolBatchItem builds the LLM-facing tool message, the memory
// message, and the compaction metadata for one dispatched call. It is also
// re-run for results annotated after a batch persist failure, so the
// persist error becomes visible to the model exactly like a tool error.
func (e *Engine) materializeToolBatchItem(item *toolBatchItem, profile core.LLMProfileRef) error {
	call := item.call
	contextResult, transformMeta := e.compactToolResultForContext(item.result, profile.Context.ToolResultMaxTokens)
	if maxBytes := profile.Context.ToolOutputMaxBytes; maxBytes > 0 && len(contextResult.Output) > maxBytes {
		truncated, meta := contextwindow.ApplyToolOutputTransform(call.Name, contextResult.Output, maxBytes/3, e.toolTransformsCopy())
		contextResult.Output = truncated
		transformMeta = meta
		transformMeta.Truncated = true
	}
	toolMsg := memoryMessageFromToolResult(call, contextResult)
	if transformMeta.Truncated || transformMeta.Strategy != contextwindow.TransformStrategyNone {
		if toolMsg.Metadata == nil {
			toolMsg.Metadata = map[string]string{}
		}
		toolMsg.Metadata["transformed"] = "true"
		toolMsg.Metadata["truncate_strategy"] = transformMeta.Strategy
	}
	raw, err := json.Marshal(contextResult)
	if err != nil {
		return err
	}
	class := classifyToolResultMessage(llm.Message{Role: llm.RoleTool, Content: string(raw), Name: call.Name})
	item.contextResult = contextResult
	item.transformMeta = transformMeta
	item.toolMsg = toolMsg
	item.message = llm.Message{
		Role:       llm.RoleTool,
		Content:    string(raw),
		Name:       call.Name,
		ToolCallID: call.ID,
		Metadata: map[string]string{
			"tool_result_class": string(class),
			"truncate_strategy": transformMeta.Strategy,
		},
	}
	return nil
}

// growExecutableToolPrefix returns the exclusive end index of consecutive
// calls starting at start that do not require a human-approval pause.
// start itself must already have been checked (and not paused).
func (e *Engine) growExecutableToolPrefix(ctx context.Context, runID string, calls []llm.ToolCall, start int) (int, error) {
	end := start + 1
	for end < len(calls) {
		needs, err := e.toolCallNeedsPause(ctx, runID, calls[end])
		if err != nil {
			return start, err
		}
		if needs {
			break
		}
		end++
	}
	return end, nil
}

// markPlanStepsForBatch updates plan progress for successful tools in order.
func (e *Engine) markPlanStepsForBatch(ctx context.Context, runID string, items []toolBatchItem) {
	if !e.scenario.Orchestration.Planning.Enabled || !e.scenario.Orchestration.Planning.Execute {
		return
	}
	for _, item := range items {
		if item.result.Error != "" {
			continue
		}
		if err := e.markPlanStepDone(ctx, runID, item.call.Name); err != nil {
			e.logWarn(ctx, "runtime: failed to update plan progress after successful tool call", "run_id", runID, "tool", item.call.Name, "error", err)
		}
	}
}

func formatDoomLoopError(tool string, limit int) string {
	return fmt.Sprintf("doom-loop detected: tool %q repeated with the same input %d time(s)", tool, limit)
}
