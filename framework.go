// Package agentflow provides a small public facade for embedding the
// scenario-driven agent runtime in other Go projects.
//
// Applications that need low-level extension points can import pkg/core,
// pkg/llm, pkg/memory, and pkg/runstate directly. Applications that only need
// to load a YAML scenario and run it should use this package.
package agentflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	memoryfile "github.com/aijustin/agentflow-go/internal/adapter/memory/file"
	memoryinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/inmem"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	runstatepostgres "github.com/aijustin/agentflow-go/internal/adapter/runstate/postgres"
	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	appscenario "github.com/aijustin/agentflow-go/internal/application/scenario"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
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
	scenario core.Scenario
	engine   *appexec.Engine
	runs     runstate.Repository
	blobs    runstate.BlobStore
	events   core.EventSink
	gate     core.HumanGate
	llm      llm.Gateway
	tools    map[string]core.ToolExecutor
	memory   map[string]memory.Repository
	policy   security.Policy
	audit    audit.Sink
	toolGov  governance.ToolPolicy
	redactor governance.OutputRedactor
}

type options struct {
	llm         llm.Gateway
	runs        runstate.Repository
	blobs       runstate.BlobStore
	events      core.EventSink
	gate        core.HumanGate
	tools       map[string]core.ToolExecutor
	memory      map[string]memory.Repository
	tokenSecret []byte
	tokenTTL    time.Duration
	tokenWriter io.Writer
	policy      security.Policy
	audit       audit.Sink
	toolGov     governance.ToolPolicy
	redactor    governance.OutputRedactor
}

type toolRegistry map[string]core.ToolExecutor

func (r toolRegistry) Tool(name string) (core.ToolExecutor, bool) {
	tool, ok := r[name]
	return tool, ok
}

// Option customizes Framework construction.
type Option func(*options) error

// LoadScenarioFile loads and validates a scenario YAML file.
func LoadScenarioFile(path string) (core.Scenario, error) {
	return configyaml.LoadFile(path)
}

// LoadScenario loads and validates a scenario YAML document.
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
	cfg := options{
		runs:        runstateinmem.NewRepository(),
		blobs:       blobinmem.NewStore(),
		events:      core.EventSinkFunc(func(context.Context, core.Event) error { return nil }),
		tools:       make(map[string]core.ToolExecutor),
		memory:      make(map[string]memory.Repository),
		tokenWriter: io.Discard,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	if len(cfg.tokenSecret) > 0 {
		if cfg.gate != nil {
			return nil, fmt.Errorf("agentflow: WithHumanGate and WithHITLTokenSecret are mutually exclusive")
		}
		signer, err := runstate.NewTokenSigner(cfg.tokenSecret)
		if err != nil {
			return nil, err
		}
		cfg.gate = humancli.NewGate(cfg.runs, signer, cfg.tokenWriter, humancli.WithTokenTTL(cfg.tokenTTL))
	}
	for name, ref := range scenario.Memories {
		if _, exists := cfg.memory[name]; exists {
			continue
		}
		if ref.Type == "in_memory" {
			cfg.memory[name] = memoryinmem.NewRepository()
		}
	}
	engine, err := appexec.NewEngine(scenario, appexec.Dependencies{
		LLM:            cfg.llm,
		Runs:           cfg.runs,
		Blobs:          cfg.blobs,
		Events:         cfg.events,
		HumanGate:      cfg.gate,
		Tools:          toolRegistry(cfg.tools),
		Memory:         cfg.memory,
		Policy:         cfg.policy,
		Audit:          cfg.audit,
		ToolPolicy:     cfg.toolGov,
		OutputRedactor: cfg.redactor,
	})
	if err != nil {
		return nil, err
	}
	return &Framework{
		scenario: scenario,
		engine:   engine,
		runs:     cfg.runs,
		blobs:    cfg.blobs,
		events:   cfg.events,
		gate:     cfg.gate,
		llm:      cfg.llm,
		tools:    cfg.tools,
		memory:   cfg.memory,
		policy:   cfg.policy,
		audit:    cfg.audit,
		toolGov:  cfg.toolGov,
		redactor: cfg.redactor,
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

func WithToolGovernancePolicy(policy governance.ToolPolicy) Option {
	return func(o *options) error {
		if policy == nil {
			return fmt.Errorf("agentflow: tool governance policy is nil")
		}
		o.toolGov = policy
		return nil
	}
}

func WithOutputRedactor(redactor governance.OutputRedactor) Option {
	return func(o *options) error {
		if redactor == nil {
			return fmt.Errorf("agentflow: output redactor is nil")
		}
		o.redactor = redactor
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
	if f.scenario.Orchestration.Mode == core.OrchestrationFixedWorkflow {
		return f.runWorkflow(ctx, req)
	}
	return f.engine.Run(ctx, req)
}

func (f *Framework) runWorkflow(ctx context.Context, req RunRequest) (RunResult, error) {
	ctx, cancel := withScenarioTimeout(ctx, f.scenario.Runtime.Timeout)
	defer cancel()
	if req.RunID == "" {
		req.RunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: f.scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input": req.Context,
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	if err := f.runs.Save(ctx, &snapshot, 0); err != nil {
		return RunResult{}, err
	}
	f.emit(ctx, core.EventRunStarted, req.RunID, nil)
	runner := orchestration.NewWorkflowRunner(
		toolRegistry(f.tools),
		f.runs,
		f.events,
		orchestration.WithHumanGate(f.gate),
		orchestration.WithBlobStore(f.blobs),
	)
	if err := runner.Run(ctx, f.scenario, req.RunID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err := f.runs.Load(ctx, req.RunID)
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

func (f *Framework) markWorkflowFailed(ctx context.Context, runID string, cause error) {
	if snapshot, err := f.runs.Load(ctx, runID); err == nil {
		snapshot.Status = runstate.RunStatusFailed
		_ = f.runs.Save(ctx, &snapshot, snapshot.Version)
	}
	f.emit(ctx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q}`, cause.Error())))
}

// RunStructured executes an agent using its configured output_schema and a
// gateway that implements llm.StructuredOutputter.
func (f *Framework) RunStructured(ctx context.Context, req RunRequest) (RunResult, error) {
	return f.engine.RunStructured(ctx, req)
}

// Stream executes an agent using a gateway that implements llm.Streamer.
func (f *Framework) Stream(ctx context.Context, req RunRequest) (<-chan llm.ChatChunk, error) {
	return f.engine.Stream(ctx, req)
}

// Resume resumes a paused run through the configured human gate.
func (f *Framework) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	if f.gate == nil {
		return fmt.Errorf("agentflow: human gate is not configured")
	}
	return f.gate.Resume(ctx, token, decision, amendment)
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

// NewFileBlobStore creates a file-backed blob store.
func NewFileBlobStore(dir string) (runstate.BlobStore, error) {
	return blobfile.NewStore(dir)
}

// NewFileMemoryRepository creates a JSON-file-backed memory repository.
func NewFileMemoryRepository(dir string) (memory.Repository, error) {
	return memoryfile.NewRepository(dir)
}
