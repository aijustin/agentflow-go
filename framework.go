// Package agentflow provides a small public facade for embedding the
// scenario-driven agent runtime in other Go projects.
//
// Applications that need low-level extension points can import pkg/core,
// pkg/llm, pkg/memory, and pkg/runstate directly. Applications that only need
// to load a YAML scenario and run it should use this package.
package agentflow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	memoryfile "github.com/aijustin/agentflow-go/internal/adapter/memory/file"
	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstatepostgres "github.com/aijustin/agentflow-go/internal/adapter/runstate/postgres"
	runstaterecording "github.com/aijustin/agentflow-go/internal/adapter/runstate/recording"
	runstateredis "github.com/aijustin/agentflow-go/internal/adapter/runstate/redis"
	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	appscenario "github.com/aijustin/agentflow-go/internal/application/scenario"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/catalog"
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

// RunRequest is the input passed to Framework.Run.
type RunRequest = appexec.RunRequest

// RunResult is the result returned from Framework.Run.
type RunResult = appexec.RunResult

// Plan is a resolved scenario plan that library users can inspect before
// creating a Framework.
type Plan struct {
	Scenario core.Scenario
	LLMs     map[string]llm.Profile
	Memory   map[string]memory.Namespace
}

// Framework is an embeddable runtime wrapper for one scenario.
type Framework struct {
	scenario          core.Scenario
	engine            *appexec.Engine
	runs              runstate.Repository
	checkpointHistory runstate.CheckpointHistory
	blobs             runstate.BlobStore
	events      core.EventSink
	gate        core.HumanGate
	tokenSigner *runstate.TokenSigner
	llm         llm.Gateway
	tools       *toolRegistry
	memory      map[string]memory.Repository
	policy      security.Policy
	audit       audit.Sink
	toolGov     governance.ToolPolicy
	redactor    governance.OutputRedactor
	recorder    observability.Recorder
	tracer      observability.Tracer
	logger      log.Logger
	closers     []func(context.Context) error
}

type options struct {
	llm               llm.Gateway
	runs              runstate.Repository
	checkpointHistory runstate.CheckpointHistory
	blobs             runstate.BlobStore
	events      core.EventSink
	gate        core.HumanGate
	tools       map[string]core.ToolExecutor
	resolver    core.ToolResolver
	memory      map[string]memory.Repository
	tierMemory  map[string]tier.Manager
	tierStores  map[string]tier.Store
	cognitive   map[string]memory.CognitiveMemory
	jobQueue    async.Queue
	tokenSecret []byte
	tokenTTL    time.Duration
	tokenWriter io.Writer
	policy      security.Policy
	audit       audit.Sink
	toolGov     governance.ToolPolicy
	redactor    governance.OutputRedactor
	recorder    observability.Recorder
	tracer      observability.Tracer
	logger      log.Logger
	requireLLM  bool
	closers     []func(context.Context) error
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
	return r.engine.RunAgent(ctx, r.name, input)
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

// LoadScenarioFile loads and validates a scenario YAML file.
//
// Deprecated: define scenarios in Go with pkg/builder or core.Scenario and call
// New instead. YAML loading is scheduled for removal in a future major release.
func LoadScenarioFile(path string) (core.Scenario, error) {
	return configyaml.LoadFile(path)
}

// LoadScenario loads and validates a scenario YAML document.
//
// Deprecated: use programmatic core.Scenario construction instead.
func LoadScenario(data []byte) (core.Scenario, error) {
	return configyaml.Load(data)
}

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

// NewFromFile loads a scenario YAML file and creates a Framework.
//
// Deprecated: use New with a builder or core.Scenario. See pkg/builder and
// docs/product-direction.md.
func NewFromFile(path string, opts ...Option) (*Framework, error) {
	scenario, err := LoadScenarioFile(path)
	if err != nil {
		return nil, err
	}
	return New(scenario, opts...)
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
		cfg.runs = &runstaterecording.Repository{Inner: cfg.runs, History: cfg.checkpointHistory}
	}
	if cfg.requireLLM {
		autoMemory := make(map[string]bool)
		for name, ref := range scenario.Memories {
			if ref.Type == "in_memory" {
				autoMemory[name] = true
			}
		}
		if err := validateWiring(scenario, cfg, autoMemory, WiringOptions{RequireLLM: true}); err != nil {
			return nil, err
		}
	}
	tools := newToolRegistry(cfg.tools, cfg.resolver)
	var tokenSigner *runstate.TokenSigner
	if len(cfg.tokenSecret) > 0 {
		if cfg.gate != nil {
			return nil, fmt.Errorf("agentflow: WithHumanGate and WithHITLTokenSecret are mutually exclusive")
		}
		signer, err := runstate.NewTokenSigner(cfg.tokenSecret)
		if err != nil {
			return nil, err
		}
		tokenSigner = signer
		cfg.gate = humancli.NewGate(cfg.runs, signer, cfg.tokenWriter, humancli.WithTokenTTL(cfg.tokenTTL))
	}
	for name, ref := range scenario.Memories {
		if _, exists := cfg.memory[name]; exists {
			continue
		}
		if ref.Tiers != nil && ref.Tiers.Enabled {
			continue
		}
		if ref.Type == "in_memory" {
			cfg.memory[name] = memoryinmem.NewRepository()
		}
	}
	for name, ref := range scenario.Memories {
		if ref.Tiers == nil || !ref.Tiers.Enabled {
			continue
		}
		if _, exists := cfg.tierMemory[name]; exists {
			continue
		}
		store := cfg.tierStores[name]
		if store == nil {
			store = tierinmem.NewStore()
		}
		settings, _ := tier.SettingsFromCore(ref.Tiers)
		manager := tier.NewManagerWithWeights(store, settings.Policy(), tierMigrationObserver(scenario, cfg.recorder, cfg.events), settings.Weights())
		cognitive := cfg.cognitive[name]
		if cognitive == nil {
			if cfg.cognitive == nil {
				cfg.cognitive = make(map[string]memory.CognitiveMemory)
			}
			cognitive = memoryinmem.NewCognitiveRepository()
			cfg.cognitive[name] = cognitive
		}
		cfg.tierMemory[name] = tier.NewDualWriteManager(manager, cognitive)
	}
	var enqueueMemoryReconcile func(context.Context, async.Job) error
	if cfg.jobQueue != nil {
		queue := cfg.jobQueue
		enqueueMemoryReconcile = func(ctx context.Context, job async.Job) error {
			_, err := queue.Enqueue(ctx, job)
			return err
		}
	}
	engine, err := appexec.NewEngine(scenario, appexec.Dependencies{
		LLM:                    cfg.llm,
		Runs:                   cfg.runs,
		Blobs:                  cfg.blobs,
		Events:                 cfg.events,
		HumanGate:              cfg.gate,
		Tools:                  tools,
		Memory:                 cfg.memory,
		TierMemory:             cfg.tierMemory,
		Cognitive:              cfg.cognitive,
		Policy:                 cfg.policy,
		Audit:                  cfg.audit,
		ToolPolicy:             cfg.toolGov,
		OutputRedactor:         cfg.redactor,
		Recorder:               cfg.recorder,
		Tracer:                 cfg.tracer,
		Logger:                 cfg.logger,
		EnqueueMemoryReconcile: enqueueMemoryReconcile,
	})
	if err != nil {
		return nil, err
	}
	return &Framework{
		scenario:          scenario,
		engine:            engine,
		runs:              cfg.runs,
		checkpointHistory: cfg.checkpointHistory,
		blobs:             cfg.blobs,
		events:      cfg.events,
		gate:        cfg.gate,
		tokenSigner: tokenSigner,
		llm:         cfg.llm,
		tools:       tools,
		memory:      cfg.memory,
		policy:      cfg.policy,
		audit:       cfg.audit,
		toolGov:     cfg.toolGov,
		redactor:    cfg.redactor,
		recorder:    cfg.recorder,
		tracer:      cfg.tracer,
		logger:      cfg.logger,
		closers:     append([]func(context.Context) error(nil), cfg.closers...),
	}, nil
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

// WithTierStore wires a tier store and builds a default manager from policy.
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
		o.tierStores[name] = store
		if o.tierMemory == nil {
			o.tierMemory = make(map[string]tier.Manager)
		}
		o.tierMemory[name] = tier.NewManager(store, policy, tier.NoopMigrationObserver{})
		return nil
	}
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

// WithHITLTokenSecret wires the built-in HMAC-token human gate using the same
// RunStateRepository as the framework. tokenWriter can be nil.
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

// WithHITLTokenTTL sets the lifetime for tokens emitted by WithHITLTokenSecret.
func WithHITLTokenTTL(ttl time.Duration) Option {
	return func(o *options) error {
		if ttl < 0 {
			return fmt.Errorf("agentflow: HITL token ttl must be >= 0")
		}
		o.tokenTTL = ttl
		return nil
	}
}

// Run executes the framework scenario.
func (f *Framework) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		return f.runWorkflow(ctx, req)
	case core.OrchestrationHybrid:
		return f.runHybrid(ctx, req)
	default:
		return f.engine.Run(ctx, req)
	}
}

func (f *Framework) runWorkflow(ctx context.Context, req RunRequest) (RunResult, error) {
	return f.runWorkflowScenario(ctx, f.scenario, req)
}

func (f *Framework) runWorkflowScenario(ctx context.Context, scenario core.Scenario, req RunRequest) (RunResult, error) {
	ctx, cancel := withScenarioTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input": req.Context,
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	saveRunResumeMetadata(&snapshot, req)
	runstate.StampTenant(ctx, &snapshot)
	if err := f.runs.Save(ctx, &snapshot, 0); err != nil {
		return RunResult{}, err
	}
	f.emit(ctx, core.EventRunStarted, req.RunID, nil)
	runner := f.newWorkflowRunner()
	if err := runner.Run(ctx, scenario, req.RunID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err := runstate.LoadAuthorized(ctx, f.runs, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	loaded.Status = runstate.RunStatusCompleted
	if err := f.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	f.emit(ctx, core.EventRunCompleted, req.RunID, nil)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: "fixed workflow completed"}, nil
}

// runHybrid executes a hybrid scenario: the optional fixed workflow DAG runs
// first, then an autonomous agent executes with the workflow step outputs
// injected as context.  If no workflow is defined, execution falls back to
// pure autonomous mode.
func (f *Framework) runHybrid(ctx context.Context, req RunRequest) (RunResult, error) {
	if f.scenario.Orchestration.Workflow == nil {
		return f.engine.Run(ctx, req)
	}
	req, paused, err := f.prepareHybridAutonomousRunScenario(ctx, f.scenario, req)
	if err != nil || paused.Status != "" {
		return paused, err
	}
	return f.engine.RunHybrid(ctx, req)
}

func (f *Framework) prepareHybridAutonomousRun(ctx context.Context, req RunRequest) (RunRequest, RunResult, error) {
	return f.prepareHybridAutonomousRunScenario(ctx, f.scenario, req)
}

func (f *Framework) prepareHybridAutonomousRunScenario(ctx context.Context, scenario core.Scenario, req RunRequest) (RunRequest, RunResult, error) {
	ctx, cancel := withScenarioTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input":           req.Context,
			executionPhaseVar: json.RawMessage(fmt.Sprintf("%q", executionPhaseWorkflow)),
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	saveRunResumeMetadata(&snapshot, req)
	runstate.StampTenant(ctx, &snapshot)
	if err := f.runs.Save(ctx, &snapshot, 0); err != nil {
		return req, RunResult{}, err
	}
	f.emit(ctx, core.EventRunStarted, req.RunID, nil)
	runner := f.newWorkflowRunner()
	if err := runner.Run(ctx, scenario, req.RunID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return req, RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, req.RunID, err)
		return req, RunResult{}, err
	}
	loaded, err := runstate.LoadAuthorized(ctx, f.runs, req.RunID)
	if err != nil {
		return req, RunResult{}, err
	}
	if loaded.Variables == nil {
		loaded.Variables = make(map[string]json.RawMessage)
	}
	loaded.Variables[executionPhaseVar] = json.RawMessage(fmt.Sprintf("%q", executionPhaseAutonomous))
	if err := f.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return req, RunResult{}, err
	}
	req, err = f.hydrateRunRequest(ctx, req, loaded)
	if err != nil {
		f.markWorkflowFailed(ctx, req.RunID, err)
		return req, RunResult{}, err
	}
	return req, RunResult{}, nil
}

func (f *Framework) markWorkflowFailed(ctx context.Context, runID string, cause error) {
	if snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID); err == nil {
		snapshot.Status = runstate.RunStatusFailed
		if saveErr := f.runs.Save(ctx, &snapshot, snapshot.Version); saveErr != nil {
			if f.logger != nil {
				f.logger.Warn(ctx, "agentflow: failed to persist workflow failure status", "run_id", runID, "save_error", saveErr)
			}
			f.emit(ctx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q,"save_error":%q}`, cause.Error(), saveErr.Error())))
			return
		}
	}
	f.emit(ctx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q}`, cause.Error())))
}

// RunStructured executes an agent using its configured output_schema and a
// gateway that implements llm.StructuredOutputter.
func (f *Framework) RunStructured(ctx context.Context, req RunRequest) (RunResult, error) {
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		if _, err := f.runWorkflow(ctx, req); err != nil {
			return RunResult{}, err
		}
		return f.engine.RunStructured(ctx, req)
	case core.OrchestrationHybrid:
		req, paused, err := f.prepareHybridAutonomousRun(ctx, req)
		if err != nil || paused.Status != "" {
			return paused, err
		}
		return f.engine.RunStructured(ctx, req)
	default:
		return f.engine.RunStructured(ctx, req)
	}
}

// Stream executes an agent using a gateway that implements llm.Streamer.
func (f *Framework) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		if _, err := f.runWorkflow(ctx, req); err != nil {
			return nil, err
		}
		return f.engine.Stream(ctx, req)
	case core.OrchestrationHybrid:
		req, paused, err := f.prepareHybridAutonomousRun(ctx, req)
		if err != nil {
			return nil, err
		}
		if paused.Status == runstate.RunStatusPaused {
			return nil, fmt.Errorf("agentflow: workflow paused at token %q", paused.Token)
		}
		return f.engine.Stream(ctx, req)
	default:
		return f.engine.Stream(ctx, req)
	}
}

// Resume resumes a paused run through the configured human gate.
func (f *Framework) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	if f.gate == nil {
		return fmt.Errorf("agentflow: human gate is not configured")
	}
	return f.gate.Resume(ctx, token, decision, amendment)
}

func (f *Framework) Catalog() catalog.Catalog {
	return catalog.FromScenario(f.scenario)
}

// Scenario returns the scenario used by this framework.
func (f *Framework) Scenario() core.Scenario {
	return f.scenario
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
	payload = governance.RedactEventPayload(ctx, f.redactor, runID, typ, payload)
	_ = f.events.Emit(ctx, core.Event{
		Type:         typ,
		RunID:        runID,
		ScenarioName: f.scenario.Name,
		Timestamp:    time.Now().UTC(),
		Payload:      payload,
	})
}

func withScenarioTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// NewInMemoryRunStateRepository creates the default in-memory run-state
// repository used by New.
func NewInMemoryRunStateRepository() runstate.Repository {
	return runstateinmem.NewRepository()
}

// NewInMemoryCheckpointHistory creates an append-only in-memory checkpoint history store.
func NewInMemoryCheckpointHistory() runstate.CheckpointHistory {
	return runstateinmem.NewCheckpointHistory()
}

// NewPostgresCheckpointHistory creates a PostgreSQL append-only checkpoint history store.
func NewPostgresCheckpointHistory(db *sql.DB, tableName ...string) (runstate.CheckpointHistory, error) {
	if len(tableName) > 1 {
		return nil, fmt.Errorf("agentflow: at most one postgres checkpoint history table name is allowed")
	}
	if len(tableName) == 1 && tableName[0] != "" {
		return runstatepostgres.NewCheckpointHistory(db, runstatepostgres.WithCheckpointHistoryTable(tableName[0]))
	}
	return runstatepostgres.NewCheckpointHistory(db)
}

// NewInMemoryBlobStore creates the default in-memory blob store used by New.
func NewInMemoryBlobStore() runstate.BlobStore {
	return blobinmem.NewStore()
}

// NewFileRunStateRepository creates a JSON-file-backed run-state repository.
func NewFileRunStateRepository(dir string) (runstate.Repository, error) {
	return runstatefile.NewRepository(dir)
}

// NewPostgresRunStateRepository creates a PostgreSQL-compatible run-state
// repository using a caller-provided *sql.DB. Applications must import and
// register their preferred PostgreSQL database/sql driver.
func NewPostgresRunStateRepository(db *sql.DB, tableName ...string) (runstate.Repository, error) {
	if len(tableName) > 1 {
		return nil, fmt.Errorf("agentflow: at most one postgres run-state table name is allowed")
	}
	if len(tableName) == 1 && tableName[0] != "" {
		return runstatepostgres.NewRepository(db, runstatepostgres.WithTableName(tableName[0]))
	}
	return runstatepostgres.NewRepository(db)
}

type RedisRunStateRepositoryConfig struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewRedisRunStateRepository creates a Redis-backed run-state repository with
// compare-and-swap version checks for distributed workers.
func NewRedisRunStateRepository(config RedisRunStateRepositoryConfig) (runstate.Repository, error) {
	return runstateredis.NewRepository(runstateredis.Config{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		KeyPrefix:    config.KeyPrefix,
		DialTimeout:  config.DialTimeout,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	})
}

// NewFileBlobStore creates a file-backed blob store.
func NewFileBlobStore(dir string) (runstate.BlobStore, error) {
	return blobfile.NewStore(dir)
}

// NewFileMemoryRepository creates a JSON-file-backed memory repository.
func NewFileMemoryRepository(dir string) (memory.Repository, error) {
	return memoryfile.NewRepository(dir)
}

// generateRunID returns a cryptographically random run identifier with a
// "run-" prefix.  It falls back to a nanosecond timestamp on the rare occasion
// that the random reader fails.
func generateRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
