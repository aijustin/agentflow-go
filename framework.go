package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	tierllmsummary "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/llmsummary"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstaterecording "github.com/aijustin/agentflow-go/internal/adapter/runstate/recording"
	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	appscenario "github.com/aijustin/agentflow-go/internal/application/scenario"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/catalog"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/interjection"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

// RunRequest is the input passed to Framework.Run.
type RunRequest = appexec.RunRequest

// TrustMode controls run-scoped tool approval overrides (e.g. full_trust).
type TrustMode = appexec.TrustMode

const (
	TrustModeDefault   = appexec.TrustModeDefault
	TrustModeFullTrust = appexec.TrustModeFullTrust
)

// RunResult is the result returned from Framework.Run.
type RunResult = appexec.RunResult

// Classified run-state errors, re-exported from the runtime so callers of
// every orchestration mode's entry points can branch on them with errors.Is.
var (
	ErrRunAlreadyCompleted = appexec.ErrRunAlreadyCompleted
	ErrRunInProgress       = appexec.ErrRunInProgress
	ErrRunPaused           = appexec.ErrRunPaused
	ErrRunFailed           = appexec.ErrRunFailed
	ErrRunCancelled        = appexec.ErrRunCancelled
	// ErrResumeInProgress reports that another caller is already resuming or
	// continuing this run in this process. A concurrent ResumeAndContinue /
	// ResumeRunByID / ContinueRun on the same run fails fast with it instead
	// of racing the pause token and surfacing an ambiguous ErrTokenSuperseded.
	ErrResumeInProgress = runstate.ErrResumeInProgress
)

// Plan is a resolved scenario plan that library users can inspect before
// creating a Framework.
type Plan struct {
	Scenario core.Scenario
	LLMs     map[string]llm.Profile
	Memory   map[string]memory.Namespace
}

// Framework is an embeddable runtime wrapper for one scenario.
type Framework struct {
	mu sync.RWMutex

	scenario               core.Scenario
	engine                 *appexec.Engine
	runs                   runstate.Repository
	checkpointHistory      runstate.CheckpointHistory
	blobs                  runstate.BlobStore
	events                 core.EventSink
	gate                   core.HumanGate
	approvalEvaluator      core.ToolApprovalEvaluator
	tokenSigner            *runstate.TokenSigner
	tokenTTL               time.Duration
	llm                    llm.Gateway
	tools                  *toolRegistry
	memory                 map[string]memory.Repository
	tierMemory             map[string]tier.Manager
	cognitive              map[string]memory.CognitiveMemory
	tierStores             map[string]tier.Store
	tierStorePolicies      map[string]tier.Policy
	tierColdIndexers       map[string]tier.ColdSummaryIndexer
	tierColdSummarizers    map[string]tier.ContentSummarizer
	enqueueMemoryReconcile func(context.Context, async.Job) error
	toolOrchestrator       toolorch.ToolOrchestrator
	approvalStore          toolorch.ApprovalStore
	turnStopHook           core.TurnStopHook
	resumeAuthHook         ResumeAuthorizationHook
	policy                 security.Policy
	audit                  audit.Sink
	toolGov                governance.ToolPolicy
	redactor               governance.OutputRedactor
	recorder               observability.Recorder
	tracer                 observability.Tracer
	logger                 log.Logger
	runLocker              coordination.Locker
	runLeaseOwner          string
	runLeaseTTL            time.Duration
	workflowRunner         *orchestration.WorkflowRunner
	closers                []func(context.Context) error

	// resumeMu guards resumeInFlight, the in-process set of runs currently
	// being resumed/continued. It gives concurrent ResumeAndContinue /
	// ResumeRunByID / ContinueRun calls on the same run a deterministic
	// ErrResumeInProgress loser even when no distributed run lease is
	// configured.
	resumeMu       sync.Mutex
	resumeInFlight map[string]struct{}
}

type options struct {
	llm                  llm.Gateway
	runs                 runstate.Repository
	checkpointHistory    runstate.CheckpointHistory
	blobs                runstate.BlobStore
	events               core.EventSink
	gate                 core.HumanGate
	approvalEvaluator    core.ToolApprovalEvaluator
	tools                map[string]core.ToolExecutor
	resolver             core.ToolResolver
	memory               map[string]memory.Repository
	tierMemory           map[string]tier.Manager
	tierStores           map[string]tier.Store
	tierStorePolicies    map[string]tier.Policy
	tierColdIndexers     map[string]tier.ColdSummaryIndexer
	tierColdSummarizers  map[string]tier.ContentSummarizer
	cognitive            map[string]memory.CognitiveMemory
	jobQueue             async.Queue
	tokenSecret          []byte
	tokenSecretSecondary []byte
	tokenTTL             time.Duration
	tokenTTLSet          bool
	tokenWriter          io.Writer
	policy               security.Policy
	audit                audit.Sink
	toolGov              governance.ToolPolicy
	redactor             governance.OutputRedactor
	recorder             observability.Recorder
	tracer               observability.Tracer
	logger               log.Logger
	requireLLM           bool
	runLocker            coordination.Locker
	runLeaseOwner        string
	runLeaseTTL          time.Duration
	closers              []func(context.Context) error
	toolTransforms       map[string]contextwindow.ToolOutputTransform
	interjectDrain       interjection.DrainPolicy
	toolOrchestrator     toolorch.ToolOrchestrator
	approvalStore        toolorch.ApprovalStore
	turnStopHook         core.TurnStopHook
	resumeAuthHook       ResumeAuthorizationHook
}

type toolRegistry struct {
	mu       sync.Mutex
	eager    map[string]core.ToolExecutor
	cache    map[string]core.ToolExecutor
	resolver core.ToolResolver
}

type workflowAgentRegistry struct {
	agents map[string]core.Agent
	engine *appexec.Engine
}

func (r workflowAgentRegistry) Agent(name string) (core.AgentRunner, bool) {
	if _, ok := r.agents[name]; !ok {
		return nil, false
	}
	return workflowAgentRunner{name: name, engine: r.engine}, true
}

type workflowAgentRunner struct {
	name   string
	engine *appexec.Engine
}

func (r workflowAgentRunner) Run(ctx context.Context, input core.AgentInput) (core.AgentOutput, error) {
	output, err := r.engine.RunAgent(ctx, r.name, input)
	if err == nil {
		return output, nil
	}
	var paused appexec.RunPausedError
	if errors.As(err, &paused) {
		nodeID := core.WorkflowNodeFromContext(ctx)
		if nodeID == "" {
			nodeID = paused.Kind
		}
		return core.AgentOutput{}, orchestration.WorkflowPausedError{
			RunID:  paused.RunID,
			NodeID: nodeID,
			Token:  paused.Token,
		}
	}
	return core.AgentOutput{}, err
}

func newToolRegistry(eager map[string]core.ToolExecutor, resolver core.ToolResolver) *toolRegistry {
	if eager == nil {
		eager = make(map[string]core.ToolExecutor)
	}
	return &toolRegistry{eager: eager, cache: make(map[string]core.ToolExecutor), resolver: resolver}
}

func (r *toolRegistry) ResolveTool(ctx context.Context, tool core.Tool) (core.ToolExecutor, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	name := tool.Name
	if name == "" {
		return nil, false, fmt.Errorf("agentflow: tool name is required")
	}
	r.mu.Lock()
	if executor, ok := r.eager[name]; ok {
		r.mu.Unlock()
		return executor, true, nil
	}
	if executor, ok := r.cache[name]; ok {
		r.mu.Unlock()
		return executor, true, nil
	}
	resolver := r.resolver
	r.mu.Unlock()
	if resolver == nil {
		return nil, false, nil
	}
	executor, err := resolver.ResolveTool(ctx, tool)
	if err != nil {
		return nil, false, err
	}
	if executor == nil {
		return nil, false, fmt.Errorf("agentflow: tool resolver returned nil executor for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.eager[name]; ok {
		return existing, true, nil
	}
	if existing, ok := r.cache[name]; ok {
		return existing, true, nil
	}
	r.cache[name] = executor
	return executor, true, nil
}

// Option customizes Framework construction.
type Option func(*options) error

// ValidateScenario validates a scenario built programmatically.
func ValidateScenario(scenario core.Scenario) error {
	return configyaml.Validate(scenario)
}

// BuildPlan validates and resolves public LLM and memory metadata from a
// scenario. It does not create provider clients or start execution.
func BuildPlan(scenario core.Scenario) (Plan, error) {
	plan, err := appscenario.Build(scenario)
	if err != nil {
		return Plan{}, err
	}
	if err := ValidateScenario(plan.Scenario); err != nil {
		return Plan{}, err
	}
	return Plan{
		Scenario: plan.Scenario,
		LLMs:     plan.LLMs,
		Memory:   plan.Memory,
	}, nil
}

// New creates a Framework for a validated scenario. By default it wires
// in-memory run-state and blob stores and a no-op event sink. Production
// applications should provide persistent repositories through options.
func New(scenario core.Scenario, opts ...Option) (*Framework, error) {
	plan, err := appscenario.Build(scenario)
	if err != nil {
		return nil, err
	}
	scenario = plan.Scenario
	if err := ValidateScenario(scenario); err != nil {
		return nil, err
	}
	cfg := defaultOptions()
	cfg.runs = runstateinmem.NewRepository()
	cfg.blobs = blobinmem.NewStore()
	cfg.events = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	if cfg.checkpointHistory != nil {
		cfg.runs = &runstaterecording.Repository{Inner: cfg.runs, History: cfg.checkpointHistory, Logger: cfg.logger}
	}
	autoMemory := autoMemoryNames(scenario)
	wiringRules := defaultWiringOptions()
	if cfg.requireLLM {
		wiringRules.RequireLLM = true
	}
	if err := validateWiring(scenario, cfg, autoMemory, wiringRules); err != nil {
		return nil, err
	}
	tools := newToolRegistry(cfg.tools, cfg.resolver)
	var tokenSigner *runstate.TokenSigner
	if len(cfg.tokenSecret) > 0 {
		if cfg.gate != nil {
			return nil, fmt.Errorf("agentflow: WithHumanGate and WithHITLTokenSecret are mutually exclusive")
		}
		var signer *runstate.TokenSigner
		var err error
		if len(cfg.tokenSecretSecondary) > 0 {
			signer, err = runstate.NewTokenSignerWithRotation(cfg.tokenSecret, cfg.tokenSecretSecondary)
		} else {
			signer, err = runstate.NewTokenSigner(cfg.tokenSecret)
		}
		if err != nil {
			return nil, err
		}
		tokenSigner = signer
		// A leaked resume token for a run that is never resumed would
		// otherwise stay valid forever, so expiry is on by default and only
		// an explicit WithHITLTokenTTL(0) disables it.
		if !cfg.tokenTTLSet {
			cfg.tokenTTL = defaultHITLTokenTTL
		}
		cfg.gate = humancli.NewGate(cfg.runs, signer, cfg.tokenWriter, humancli.WithTokenTTL(cfg.tokenTTL))
	}
	if err := wireTierMemory(scenario, &cfg); err != nil {
		return nil, err
	}
	var enqueueMemoryReconcile func(context.Context, async.Job) error
	if cfg.jobQueue != nil {
		queue := cfg.jobQueue
		enqueueMemoryReconcile = func(ctx context.Context, job async.Job) error {
			_, err := queue.Enqueue(ctx, job)
			return err
		}
	}
	fw := &Framework{
		runs:                   cfg.runs,
		checkpointHistory:      cfg.checkpointHistory,
		blobs:                  cfg.blobs,
		events:                 cfg.events,
		gate:                   cfg.gate,
		approvalEvaluator:      cfg.approvalEvaluator,
		tokenSigner:            tokenSigner,
		tokenTTL:               cfg.tokenTTL,
		llm:                    cfg.llm,
		tools:                  tools,
		memory:                 cfg.memory,
		tierMemory:             cfg.tierMemory,
		cognitive:              cfg.cognitive,
		tierStores:             cfg.tierStores,
		tierStorePolicies:      cfg.tierStorePolicies,
		tierColdIndexers:       cfg.tierColdIndexers,
		tierColdSummarizers:    cfg.tierColdSummarizers,
		enqueueMemoryReconcile: enqueueMemoryReconcile,
		toolOrchestrator:       cfg.toolOrchestrator,
		approvalStore:          cfg.approvalStore,
		turnStopHook:           cfg.turnStopHook,
		resumeAuthHook:         cfg.resumeAuthHook,
		policy:                 cfg.policy,
		audit:                  cfg.audit,
		toolGov:                cfg.toolGov,
		redactor:               cfg.redactor,
		recorder:               cfg.recorder,
		tracer:                 cfg.tracer,
		logger:                 cfg.logger,
		runLocker:              cfg.runLocker,
		runLeaseOwner:          cfg.runLeaseOwner,
		runLeaseTTL:            cfg.runLeaseTTL,
		closers:                append([]func(context.Context) error(nil), cfg.closers...),
	}
	engine, err := appexec.NewEngine(scenario, fw.engineDependencies(cfg.toolTransforms, cfg.interjectDrain))
	if err != nil {
		return nil, err
	}
	fw.scenario = scenario
	fw.engine = engine
	return fw, nil
}

// WithLLMGateway wires a provider-neutral LLM gateway.
func WithLLMGateway(gateway llm.Gateway) Option {
	return func(o *options) error {
		o.llm = gateway
		return nil
	}
}

// WithRunStateRepository wires run-state persistence used for pause/resume.
func WithRunStateRepository(repo runstate.Repository) Option {
	return func(o *options) error {
		if repo == nil {
			return fmt.Errorf("agentflow: run-state repository is nil")
		}
		o.runs = repo
		return nil
	}
}

// WithCheckpointHistory wires append-only run snapshot history for time-travel.
func WithCheckpointHistory(history runstate.CheckpointHistory) Option {
	return func(o *options) error {
		if history == nil {
			return fmt.Errorf("agentflow: checkpoint history is nil")
		}
		o.checkpointHistory = history
		return nil
	}
}

// WithBlobStore wires storage for large step outputs.
func WithBlobStore(store runstate.BlobStore) Option {
	return func(o *options) error {
		if store == nil {
			return fmt.Errorf("agentflow: blob store is nil")
		}
		o.blobs = store
		return nil
	}
}

// WithEventSink wires observability event output.
func WithEventSink(sink core.EventSink) Option {
	return func(o *options) error {
		if sink == nil {
			return fmt.Errorf("agentflow: event sink is nil")
		}
		o.events = sink
		return nil
	}
}

// WithSecurityPolicy wires an authorization policy used by runtime execution.
func WithSecurityPolicy(policy security.Policy) Option {
	return func(o *options) error {
		if policy == nil {
			return fmt.Errorf("agentflow: security policy is nil")
		}
		o.policy = policy
		return nil
	}
}

// WithAuditSink wires an audit sink used for compliance-oriented events.
func WithAuditSink(sink audit.Sink) Option {
	return func(o *options) error {
		if sink == nil {
			return fmt.Errorf("agentflow: audit sink is nil")
		}
		o.audit = sink
		return nil
	}
}

// WithToolGovernancePolicy wires a per-invocation tool governance policy.
// The policy is evaluated before every tool execution and can deny calls
// based on side-effect level, call budget, or custom logic.
func WithToolGovernancePolicy(policy governance.ToolPolicy) Option {
	return func(o *options) error {
		if policy == nil {
			return fmt.Errorf("agentflow: tool governance policy is nil")
		}
		o.toolGov = policy
		return nil
	}
}

// WithOutputRedactor wires an output redactor that scrubs sensitive fields
// from step outputs before they are persisted or returned to callers.
func WithOutputRedactor(redactor governance.OutputRedactor) Option {
	return func(o *options) error {
		if redactor == nil {
			return fmt.Errorf("agentflow: output redactor is nil")
		}
		o.redactor = redactor
		return nil
	}
}

// WithLogger wires a structured logger that receives warning and error
// messages from the runtime. If not provided, messages are silently discarded.
func WithLogger(logger log.Logger) Option {
	return func(o *options) error {
		if logger == nil {
			return fmt.Errorf("agentflow: logger is nil")
		}
		o.logger = logger
		return nil
	}
}

// WithRecorder wires a metrics recorder.  If not provided, metrics are
// discarded via observability.NoopRecorder.
func WithRecorder(recorder observability.Recorder) Option {
	return func(o *options) error {
		if recorder == nil {
			return fmt.Errorf("agentflow: recorder is nil")
		}
		o.recorder = recorder
		return nil
	}
}

// WithTracer wires a distributed-tracing provider.  If not provided, tracing
// is a no-op via observability.NoopTracer.
func WithTracer(tracer observability.Tracer) Option {
	return func(o *options) error {
		if tracer == nil {
			return fmt.Errorf("agentflow: tracer is nil")
		}
		o.tracer = tracer
		return nil
	}
}

// WithHumanGate wires a custom human-in-the-loop gate.
func WithHumanGate(gate core.HumanGate) Option {
	return func(o *options) error {
		if gate == nil {
			return fmt.Errorf("agentflow: human gate is nil")
		}
		o.gate = gate
		return nil
	}
}

// WithToolApprovalEvaluator wires dynamic tool approval evaluation beyond static
// scenario Tool.Approval policies.
func WithToolApprovalEvaluator(evaluator core.ToolApprovalEvaluator) Option {
	return func(o *options) error {
		o.approvalEvaluator = evaluator
		return nil
	}
}

// WithInterjectDrainPolicy controls when Framework.Interject messages enter the
// autonomous tool loop (Codex-style steer drain alignment).
func WithInterjectDrainPolicy(policy interjection.DrainPolicy) Option {
	return func(o *options) error {
		o.interjectDrain = policy.Normalize()
		return nil
	}
}

// WithToolOrchestrator wires approval-cache / post-attempt orchestration.
// OS sandbox escalate remains host-owned via AttemptResult.
func WithToolOrchestrator(orch toolorch.ToolOrchestrator) Option {
	return func(o *options) error {
		if orch == nil {
			return fmt.Errorf("agentflow: tool orchestrator is nil")
		}
		o.toolOrchestrator = orch
		return nil
	}
}

// WithApprovalStore wires a session/run-scoped approval decision cache.
func WithApprovalStore(store toolorch.ApprovalStore) Option {
	return func(o *options) error {
		if store == nil {
			return fmt.Errorf("agentflow: approval store is nil")
		}
		o.approvalStore = store
		return nil
	}
}

// WithTurnStopHook registers a host callback that may veto turn completion
// and inject a continuation prompt (Codex stop-hooks style).
func WithTurnStopHook(hook core.TurnStopHook) Option {
	return func(o *options) error {
		if hook == nil {
			return fmt.Errorf("agentflow: turn stop hook is nil")
		}
		o.turnStopHook = hook
		return nil
	}
}

// WithToolOutputTransform registers a per-tool reshaper applied before LLM and
// memory persistence when ToolResultMaxTokens / ToolOutputMaxBytes apply.
func WithToolOutputTransform(tool string, fn contextwindow.ToolOutputTransform) Option {
	return func(o *options) error {
		if tool == "" {
			return fmt.Errorf("agentflow: tool output transform name is required")
		}
		if fn == nil {
			return fmt.Errorf("agentflow: tool output transform for %q is nil", tool)
		}
		if o.toolTransforms == nil {
			o.toolTransforms = make(map[string]contextwindow.ToolOutputTransform)
		}
		o.toolTransforms[tool] = fn
		return nil
	}
}

// WithToolExecutor registers an executable tool implementation by scenario
// tool name. Agent tool policies still come from the scenario YAML.
func WithToolExecutor(name string, executor core.ToolExecutor) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: tool name is required")
		}
		if executor == nil {
			return fmt.Errorf("agentflow: tool %q executor is nil", name)
		}
		if o.tools == nil {
			o.tools = make(map[string]core.ToolExecutor)
		}
		if _, exists := o.tools[name]; exists {
			return fmt.Errorf("agentflow: tool %q already registered", name)
		}
		o.tools[name] = executor
		return nil
	}
}

// WithToolResolver wires a resolver that creates or retrieves tool executors
// only when a declared tool is invoked. Explicit WithToolExecutor registrations
// take precedence over the resolver.
func WithToolResolver(resolver core.ToolResolver) Option {
	return func(o *options) error {
		if resolver == nil {
			return fmt.Errorf("agentflow: tool resolver is nil")
		}
		o.resolver = resolver
		return nil
	}
}

// WithMemoryRepository wires a memory backend by scenario memory name.
func WithMemoryRepository(name string, repo memory.Repository) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: memory name is required")
		}
		if repo == nil {
			return fmt.Errorf("agentflow: memory %q repository is nil", name)
		}
		if o.memory == nil {
			o.memory = make(map[string]memory.Repository)
		}
		o.memory[name] = repo
		return nil
	}
}

// WithTierMemory wires a tier manager by scenario memory name.
func WithTierMemory(name string, manager tier.Manager) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: tier memory name is required")
		}
		if manager == nil {
			return fmt.Errorf("agentflow: tier memory %q manager is nil", name)
		}
		if o.tierMemory == nil {
			o.tierMemory = make(map[string]tier.Manager)
		}
		o.tierMemory[name] = manager
		return nil
	}
}

// WithTierStore wires a tier store and the policy used to build its default
// manager. The supplied policy overrides the policy derived from the scenario
// memory tier settings for this memory name.
func WithTierStore(name string, store tier.Store, policy tier.Policy) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: tier store name is required")
		}
		if store == nil {
			return fmt.Errorf("agentflow: tier store %q is nil", name)
		}
		if o.tierStores == nil {
			o.tierStores = make(map[string]tier.Store)
		}
		if o.tierStorePolicies == nil {
			o.tierStorePolicies = make(map[string]tier.Policy)
		}
		o.tierStores[name] = store
		o.tierStorePolicies[name] = policy
		return nil
	}
}

// WithTierColdSummaryIndexer wires a vector indexer for cold-tier summary recall on a memory name.
func WithTierColdSummaryIndexer(name string, indexer tier.ColdSummaryIndexer) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: tier cold summary memory name is required")
		}
		if indexer == nil {
			return fmt.Errorf("agentflow: tier cold summary indexer for %q is nil", name)
		}
		if o.tierColdIndexers == nil {
			o.tierColdIndexers = make(map[string]tier.ColdSummaryIndexer)
		}
		o.tierColdIndexers[name] = indexer
		return nil
	}
}

// WithTierColdSummarizer wires an LLM summarizer for cold-tier archive on a memory name.
func WithTierColdSummarizer(name string, summarizer tier.ContentSummarizer) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: tier cold summarizer memory name is required")
		}
		if summarizer == nil {
			return fmt.Errorf("agentflow: tier cold summarizer for %q is nil", name)
		}
		if o.tierColdSummarizers == nil {
			o.tierColdSummarizers = make(map[string]tier.ContentSummarizer)
		}
		o.tierColdSummarizers[name] = summarizer
		return nil
	}
}

func tierColdSummarizer(cfg *options, name string, settings tier.ColdSummarySettings) tier.ContentSummarizer {
	if cfg.tierColdSummarizers != nil {
		if summarizer, ok := cfg.tierColdSummarizers[name]; ok {
			return summarizer
		}
	}
	profile := strings.TrimSpace(settings.SummaryProfile)
	if profile == "" || cfg.llm == nil {
		return nil
	}
	return tierllmsummary.NewSummarizer(cfg.llm, profile)
}

func tierColdSummaryBackend(settings tier.ColdSummarySettings, indexer tier.ColdSummaryIndexer, summarizer tier.ContentSummarizer) tier.ColdSummaryBackend {
	if !settings.Enabled {
		return tier.NoopColdSummaryBackend{}
	}
	return tier.TruncateColdSummaryBackend{Settings: settings, Vector: indexer, Summarizer: summarizer}
}

// WithCognitiveMemory wires a cognitive memory backend by scenario memory name.
func WithCognitiveMemory(name string, repo memory.CognitiveMemory) Option {
	return func(o *options) error {
		if name == "" {
			return fmt.Errorf("agentflow: cognitive memory name is required")
		}
		if repo == nil {
			return fmt.Errorf("agentflow: cognitive memory %q repository is nil", name)
		}
		if o.cognitive == nil {
			o.cognitive = make(map[string]memory.CognitiveMemory)
		}
		o.cognitive[name] = repo
		return nil
	}
}

// WithJobQueue wires an async queue used to enqueue memory.reconcile jobs after tier writes.
func WithJobQueue(queue async.Queue) Option {
	return func(o *options) error {
		if queue == nil {
			return fmt.Errorf("agentflow: job queue is nil")
		}
		o.jobQueue = queue
		return nil
	}
}

func tierMigrationObserver(scenario core.Scenario, recorder observability.Recorder, events core.EventSink) tier.MigrationObserver {
	observers := make([]tier.MigrationObserver, 0, 2)
	if recorder != nil {
		observers = append(observers, tier.MetricsObserver{Recorder: recorder, Scenario: scenario.Name})
	}
	if events != nil {
		observers = append(observers, tier.EventSinkMigrationObserver{Sink: events, Scenario: scenario.Name})
	}
	return tier.ChainMigrationObservers(observers...)
}

// ResumeAuthorizationHook authorizes a ResumeRunByID call before the
// framework mints a fresh HITL token for runID. A nil error allows the
// resume; any non-nil error aborts it and is returned to the caller.
type ResumeAuthorizationHook func(ctx context.Context, runID string) error

// WithResumeAuthorizationHook installs the authorization hook consulted by
// ResumeRunByID before it signs a new resume token. Without a hook the
// library keeps its historical behavior (any caller that knows the run ID
// may resume), so every HTTP exposure of ResumeRunByID must be authorized
// out-of-band — see the warning on ResumeRunByID.
func WithResumeAuthorizationHook(hook ResumeAuthorizationHook) Option {
	return func(o *options) error {
		if hook == nil {
			return fmt.Errorf("agentflow: resume authorization hook is nil")
		}
		o.resumeAuthHook = hook
		return nil
	}
}

// WithHITLTokenSecret wires the built-in HMAC-token human gate using the same
// RunStateRepository as the framework. tokenWriter can be nil. The secret
// must be at least runstate.MinTokenSecretLength bytes.
func WithHITLTokenSecret(secret []byte, tokenWriter io.Writer) Option {
	return func(o *options) error {
		if len(secret) == 0 {
			return fmt.Errorf("agentflow: HITL token secret is required")
		}
		o.tokenSecret = append([]byte(nil), secret...)
		if tokenWriter != nil {
			o.tokenWriter = tokenWriter
		}
		return nil
	}
}

// WithHITLTokenRotation wires the built-in human gate with a rotating key
// pair: new resume tokens are signed with primary, while tokens signed by
// either primary or secondary still verify. Deploy with (new, old), wait for
// in-flight tokens to drain, then switch back to WithHITLTokenSecret(new) —
// no token is invalidated mid-rotation. Both secrets must be at least
// runstate.MinTokenSecretLength bytes.
func WithHITLTokenRotation(primary, secondary []byte, tokenWriter io.Writer) Option {
	return func(o *options) error {
		if len(primary) == 0 || len(secondary) == 0 {
			return fmt.Errorf("agentflow: HITL token rotation requires both primary and secondary secrets")
		}
		o.tokenSecret = append([]byte(nil), primary...)
		o.tokenSecretSecondary = append([]byte(nil), secondary...)
		if tokenWriter != nil {
			o.tokenWriter = tokenWriter
		}
		return nil
	}
}

// defaultHITLTokenTTL bounds how long a HITL resume token stays valid when
// no explicit TTL is configured.
const defaultHITLTokenTTL = 24 * time.Hour

// WithHITLTokenTTL sets the lifetime for tokens emitted by
// WithHITLTokenSecret. Without this option tokens expire after 24 hours;
// pass 0 explicitly to issue tokens that never expire.
func WithHITLTokenTTL(ttl time.Duration) Option {
	return func(o *options) error {
		if ttl < 0 {
			return fmt.Errorf("agentflow: HITL token ttl must be >= 0")
		}
		o.tokenTTL = ttl
		o.tokenTTLSet = true
		return nil
	}
}

// Run executes the framework scenario.
func (f *Framework) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	ctx, release, err := f.acquireRunLease(ctx, &req)
	if err != nil {
		return RunResult{}, err
	}
	defer release()
	var result RunResult
	switch f.currentScenario().Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		result, err = f.runWorkflow(ctx, req)
	case core.OrchestrationHybrid:
		result, err = f.runHybrid(ctx, req)
	default:
		result, err = f.currentEngine().Run(ctx, req)
	}
	return result, mapLeaseLostError(ctx, err)
}

// RunStructured executes an agent using its configured output_schema and a
// gateway that implements llm.StructuredOutputter.
func (f *Framework) RunStructured(ctx context.Context, req RunRequest) (RunResult, error) {
	ctx, release, err := f.acquireRunLease(ctx, &req)
	if err != nil {
		return RunResult{}, err
	}
	defer release()
	var result RunResult
	switch f.currentScenario().Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		if workflowContainsAgentNode(f.currentScenario()) {
			return RunResult{}, fmt.Errorf("agentflow: RunStructured on fixed_workflow with agent nodes would re-execute agents; use hybrid mode or call Run")
		}
		result, err = f.runWorkflow(ctx, req)
		if err != nil {
			return RunResult{}, mapLeaseLostError(ctx, err)
		}
		if result.Status == runstate.RunStatusPaused {
			return result, nil
		}
		req, err = f.hydrateRunRequestForRunID(ctx, req)
		if err != nil {
			return RunResult{}, err
		}
		result, err = f.currentEngine().RunStructured(ctx, req)
	case core.OrchestrationHybrid:
		if f.currentScenario().Orchestration.Workflow == nil {
			result, err = f.currentEngine().RunStructured(ctx, req)
			break
		}
		var cancel context.CancelFunc
		ctx, cancel = withScenarioTimeout(ctx, f.currentScenario().Runtime.Timeout)
		defer cancel()
		var paused RunResult
		req, paused, err = f.prepareHybridAutonomousRunScenario(ctx, f.currentScenario(), req)
		if err != nil || paused.Status != "" {
			return paused, mapLeaseLostError(ctx, err)
		}
		result, err = f.currentEngine().RunStructured(ctx, req)
	default:
		result, err = f.currentEngine().RunStructured(ctx, req)
	}
	return result, mapLeaseLostError(ctx, err)
}

// Stream executes an agent using a gateway that implements llm.Streamer.
// Callers must drain the returned channel to completion or cancel ctx;
// otherwise the engine goroutine (and any run lease renewer) may remain
// blocked indefinitely.
func (f *Framework) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	ctx, release, err := f.acquireRunLease(ctx, &req)
	if err != nil {
		return nil, err
	}
	source, err := f.streamScenario(ctx, req)
	if err != nil {
		release()
		return nil, mapLeaseLostError(ctx, err)
	}
	return f.releaseLeaseOnStreamClose(ctx, source, release), nil
}

func (f *Framework) streamScenario(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	switch f.currentScenario().Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		if workflowContainsAgentNode(f.currentScenario()) {
			return nil, fmt.Errorf("agentflow: Stream on fixed_workflow with agent nodes would re-execute agents; use hybrid mode or call Run")
		}
		result, err := f.runWorkflow(ctx, req)
		if err != nil {
			return nil, err
		}
		if result.Status == runstate.RunStatusPaused {
			return pausedChunkChannel(result.Token, "workflow"), nil
		}
		req, err = f.hydrateRunRequestForRunID(ctx, req)
		if err != nil {
			return nil, err
		}
		return f.currentEngine().Stream(ctx, req)
	case core.OrchestrationHybrid:
		if f.currentScenario().Orchestration.Workflow == nil {
			return f.currentEngine().Stream(ctx, req)
		}
		// Timeout applies only to the synchronous workflow prepare phase.
		// engine.Stream owns its own timeout for the async consumer goroutine;
		// wrapping the parent ctx here would cancel the stream as soon as this
		// function returns (defer cancel).
		prepareCtx, cancel := withScenarioTimeout(ctx, f.currentScenario().Runtime.Timeout)
		req, paused, err := f.prepareHybridAutonomousRunScenario(prepareCtx, f.currentScenario(), req)
		cancel()
		if err != nil {
			return nil, err
		}
		if paused.Status == runstate.RunStatusPaused {
			return pausedChunkChannel(paused.Token, "workflow"), nil
		}
		return f.currentEngine().Stream(ctx, req)
	default:
		return f.currentEngine().Stream(ctx, req)
	}
}

// releaseLeaseOnStreamClose forwards chunks from source and releases the run
// lease when the stream ends. Stream execution outlives the Stream call
// itself (the engine's goroutine keeps running while the caller consumes the
// channel), so releasing via defer inside Stream would drop the lease while
// the run is still actively executing.
//
// Callers must drain the returned channel or cancel ctx; otherwise this
// forwarder and the lease renewer stay alive.
func (f *Framework) releaseLeaseOnStreamClose(ctx context.Context, source <-chan llm.ChatChunk, release func()) <-chan llm.ChatChunk {
	if f.runLocker == nil {
		return source
	}
	out := make(chan llm.ChatChunk)
	go func() {
		defer close(out)
		defer release()
		for chunk := range source {
			select {
			case out <- chunk:
			case <-ctx.Done():
				// Drain source so the engine goroutine can finish its own
				// cancellation bookkeeping before the lease is dropped.
				for range source {
				}
				return
			}
		}
	}()
	return out
}

// pausedChunkChannel reports a workflow/hybrid pause the same way
// engine.Stream already reports a tool-approval pause: as a single terminal
// chunk carrying the pause token and kind, instead of an error. An error
// forces callers to string-parse the pause token out of an error message
// with no structured way to resume, unlike Run/RunStructured which return a
// RunResult{Status: Paused, Token: ...}.
func pausedChunkChannel(token, kind string) <-chan llm.ChatChunk {
	ch := make(chan llm.ChatChunk, 1)
	ch <- llm.ChatChunk{Done: true, Paused: true, PauseToken: token, PauseKind: kind}
	close(ch)
	return ch
}

// Resume approves or rejects a paused run via the human gate without continuing
// execution. An approved run becomes Running with its checkpoint metadata still
// attached and no execution driver behind it; call ContinueRun (or
// ResumeAndContinue) to carry it to a terminal state. Such a run is never
// reaped by MarkAbandonedRuns, which only touches runs stamped with a lease
// owner, so the recovery window is unbounded.
func (f *Framework) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	if f.gate == nil {
		return fmt.Errorf("agentflow: human gate is not configured")
	}
	return f.gate.Resume(ctx, token, decision, amendment)
}

// Interject queues a mid-turn user message for an in-flight run. The autonomous
// tool loop drains it at the next safe point (before the next LLM call).
func (f *Framework) Interject(runID, text string) error {
	if f == nil {
		return fmt.Errorf("agentflow: framework is not initialized")
	}
	engine := f.currentEngine()
	if engine == nil {
		return fmt.Errorf("agentflow: framework is not initialized")
	}
	return engine.Interject(runID, text)
}

func (f *Framework) Catalog() catalog.Catalog {
	return catalog.FromScenario(f.currentScenario())
}

// Scenario returns the scenario used by this framework.
func (f *Framework) Scenario() core.Scenario {
	return f.currentScenario()
}

// RunStateRepository returns the repository backing run-state snapshots.
func (f *Framework) RunStateRepository() runstate.Repository {
	return f.runs
}

// BlobStore returns the blob store backing large step outputs.
func (f *Framework) BlobStore() runstate.BlobStore {
	return f.blobs
}

func (f *Framework) emit(ctx context.Context, typ core.EventType, runID string, payload json.RawMessage) {
	corr := core.EpisodeCorrelationFromContext(ctx)
	if core.IsLifecycleEvent(typ) {
		payload = core.BuildLifecyclePayload(typ, payload, corr)
	}
	payload = governance.RedactEventPayload(ctx, f.redactor, runID, typ, payload)
	event := core.Event{
		Type:         typ,
		RunID:        runID,
		ScenarioName: f.currentScenario().Name,
		EpisodeID:    corr.EpisodeID,
		SessionID:    corr.SessionID,
		TriggerKind:  corr.TriggerKind,
		Timestamp:    time.Now().UTC(),
		Category:     core.EventCategory(typ),
		DisplayLabel: core.DisplayLabel(typ),
		Payload:      payload,
	}
	if traceID, spanID := observability.TraceFromContext(ctx); traceID != "" {
		event.TraceID = traceID
		event.SpanID = spanID
	}
	if parentSpanID := observability.ParentSpanFromContext(ctx); parentSpanID != "" {
		event.ParentSpanID = parentSpanID
	}
	if err := appexec.EmitWithLifecycleRetry(ctx, f.events, event); err != nil {
		if appexec.IsCriticalLifecycleEvent(typ) {
			// A lost lifecycle event corrupts downstream state tracking, so
			// after the bounded retries this is an error, not a warning. The
			// tee below still runs: a live stream consumer should see the
			// transition even when the durable sink is down.
			errorEmitFailure(f.logger, ctx, runID, typ, err)
		} else {
			warnEmitFailure(f.logger, ctx, runID, err)
		}
	}
	if tee := appexec.EventTeeFromContext(ctx); tee != nil {
		if err := tee.Emit(ctx, event); err != nil {
			warnEmitFailure(f.logger, ctx, runID, err)
		}
	}
}

func (f *Framework) emitJSON(ctx context.Context, typ core.EventType, runID string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	f.emit(ctx, typ, runID, raw)
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

func withScenarioTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// generateRunID returns a cryptographically random run identifier with a
// "run-" prefix. The canonical implementation lives in runstate so the
// framework facade, engine, event router, and async adapter share one
// 128-bit generator instead of carrying private 64-bit copies.
func generateRunID() string {
	return runstate.GenerateRunID()
}

// --- Emit Failure Logging ---

// emitWarnGate prevents recursive Warn if the logger itself emits events.
var emitWarnGate atomic.Bool

func warnEmitFailure(logger log.Logger, ctx context.Context, runID string, err error) {
	if logger == nil || err == nil {
		return
	}
	if !emitWarnGate.CompareAndSwap(false, true) {
		return
	}
	defer emitWarnGate.Store(false)
	logger.Warn(ctx, "agentflow: event emit failed", "run_id", runID, "error", err)
}

// errorEmitFailure reports a lifecycle event that could not be delivered even
// after the bounded retries. Unlike warnEmitFailure it logs at error level:
// losing RunCompleted/RunPaused/RunFailed/RunCancelled corrupts downstream
// state tracking and must page an operator.
func errorEmitFailure(logger log.Logger, ctx context.Context, runID string, typ core.EventType, err error) {
	if logger == nil || err == nil {
		return
	}
	if !emitWarnGate.CompareAndSwap(false, true) {
		return
	}
	defer emitWarnGate.Store(false)
	logger.Error(ctx, "agentflow: lifecycle event emit failed after retries", "run_id", runID, "event_type", string(typ), "error", err)
}

// --- Async Job Handler ---

type FrameworkRunJobHandlerConfig struct {
	Framework *Framework
}

type frameworkJobHandler struct {
	framework *Framework
}

func NewFrameworkJobHandler(config FrameworkRunJobHandlerConfig) (async.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: framework is nil")
	}
	return &frameworkJobHandler{framework: config.Framework}, nil
}

func (handler *frameworkJobHandler) HandleJob(ctx context.Context, job async.Job) error {
	switch job.Type {
	case async.RunJobType:
		return handler.handleRun(ctx, job)
	case async.EventJobType:
		return handler.handleEvent(ctx, job)
	case async.ResumeContinueJobType:
		return handler.handleResumeContinue(ctx, job)
	case async.MemoryReconcileJobType:
		return handler.handleMemoryReconcile(ctx, job)
	default:
		return fmt.Errorf("agentflow: unsupported async job type %q", job.Type)
	}
}

func (handler *frameworkJobHandler) handleRun(ctx context.Context, job async.Job) error {
	var payload async.RunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode run job payload: %w", err)
		}
	}
	if payload.RunID == "" {
		payload.RunID = job.RunID
	}
	if payload.RunID == "" {
		payload.RunID = job.ID
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	// A redelivered run job for a run that already Failed cannot re-run from
	// scratch (Run rejects it with ErrRunFailed, so every queue retry would
	// fail identically until dead-letter). Re-enter through RetryFailedRun,
	// which resumes from the persisted checkpoint instead.
	if snapshot, loadErr := runstate.LoadAuthorized(ctx, handler.framework.runs, payload.RunID); loadErr == nil &&
		snapshot.Status == runstate.RunStatusFailed {
		result, err := handler.framework.RetryFailedRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		if result.Status == runstate.RunStatusPaused {
			return async.RunPausedError{RunID: result.RunID, Token: result.Token}
		}
		return nil
	}
	result, err := handler.framework.Run(ctx, RunRequest{
		RunID:   payload.RunID,
		Agent:   payload.Agent,
		Prompt:  payload.Prompt,
		Context: payload.Context,
	})
	if err != nil {
		return err
	}
	if result.Status == runstate.RunStatusPaused {
		return async.RunPausedError{RunID: result.RunID, Token: result.Token}
	}
	return nil
}

func (handler *frameworkJobHandler) handleEvent(ctx context.Context, job async.Job) error {
	var payload async.EventPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode event job payload: %w", err)
		}
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	result, err := handler.framework.HandleEvent(ctx, payload.Event())
	if err != nil {
		return err
	}
	if result.Status == runstate.RunStatusPaused {
		return async.RunPausedError{RunID: result.RunID, Token: result.Token}
	}
	return nil
}

func (handler *frameworkJobHandler) handleResumeContinue(ctx context.Context, job async.Job) error {
	var payload async.ResumeContinuePayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode resume.continue job payload: %w", err)
		}
	}
	if payload.Token == "" || !payload.Decision.Valid() {
		return fmt.Errorf("agentflow: resume.continue job requires token and valid decision")
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	_, err = handler.framework.ResumeAndContinue(ctx, payload.Token, payload.Decision, payload.Amendment)
	return err
}

func (handler *frameworkJobHandler) handleMemoryReconcile(ctx context.Context, job async.Job) error {
	var payload async.MemoryReconcilePayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("agentflow: decode memory.reconcile job payload: %w", err)
		}
	}
	if payload.MemoryName == "" || payload.Agent == "" {
		return fmt.Errorf("agentflow: memory.reconcile job requires memory_name and agent")
	}
	ctx, err := withJobPrincipal(ctx, payload.Principal)
	if err != nil {
		return err
	}
	runID := payload.RunID
	if runID == "" {
		runID = job.RunID
	}
	return handler.framework.engine.ReconcileTierMemory(ctx, runID, payload.MemoryName, payload.Agent)
}

func withJobPrincipal(ctx context.Context, principal identity.Principal) (context.Context, error) {
	if principal.ID == "" && principal.Type == "" && principal.Scope.TenantID == "" {
		return ctx, nil
	}
	if err := principal.Validate(); err != nil {
		return ctx, fmt.Errorf("agentflow: invalid async job principal: %w", err)
	}
	return identity.WithPrincipal(ctx, principal), nil
}
