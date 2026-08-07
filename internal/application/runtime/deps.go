package runtime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/aijustin/agentflow-go/internal/application/emit"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
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
