package debugui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	configyaml "github.com/aijustin/agentflow-go/internal/adapter/config/yaml"
	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	humanhttp "github.com/aijustin/agentflow-go/internal/adapter/human/http"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/openai"
	llmrouter "github.com/aijustin/agentflow-go/internal/adapter/llm/router"
	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	securityhttp "github.com/aijustin/agentflow-go/internal/adapter/security/http"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type Server struct {
	mux            *http.ServeMux
	repo           *runstateinmem.Repository
	blobs          *blobinmem.Store
	signer         *runstate.TokenSigner
	events         *EventLog
	runs           *RunStore
	httpMiddleware func(http.Handler) http.Handler
	policy         security.Policy
	audit          audit.Sink
}

type runtimeAgentRegistry struct {
	agents map[string]core.Agent
	engine *appexec.Engine
}

func (r runtimeAgentRegistry) Agent(name string) (core.AgentRunner, bool) {
	if _, ok := r.agents[name]; !ok {
		return nil, false
	}
	return runtimeAgentRunner{name: name, engine: r.engine}, true
}

type runtimeAgentRunner struct {
	name   string
	engine *appexec.Engine
}

func (r runtimeAgentRunner) Run(ctx context.Context, input core.AgentInput) (core.AgentOutput, error) {
	return r.engine.RunAgent(ctx, r.name, input)
}

type Option func(*serverOptions) error

type serverOptions struct {
	httpMiddleware func(http.Handler) http.Handler
	policy         security.Policy
	audit          audit.Sink
}

func WithHTTPMiddleware(middleware func(http.Handler) http.Handler) Option {
	return func(options *serverOptions) error {
		if middleware == nil {
			return fmt.Errorf("debugui: http middleware is nil")
		}
		options.httpMiddleware = middleware
		return nil
	}
}

func WithSecurityPolicy(policy security.Policy) Option {
	return func(options *serverOptions) error {
		if policy == nil {
			return fmt.Errorf("debugui: security policy is nil")
		}
		options.policy = policy
		return nil
	}
}

func WithAuditSink(sink audit.Sink) Option {
	return func(options *serverOptions) error {
		if sink == nil {
			return fmt.Errorf("debugui: audit sink is nil")
		}
		options.audit = sink
		return nil
	}
}

func New(secret []byte, opts ...Option) (*Server, error) {
	if len(secret) == 0 {
		secret = []byte("dev-secret")
	}
	signer, err := runstate.NewTokenSigner(secret)
	if err != nil {
		return nil, err
	}
	var options serverOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&options); err != nil {
			return nil, err
		}
	}
	s := &Server{
		mux:            http.NewServeMux(),
		repo:           runstateinmem.NewRepository(),
		blobs:          blobinmem.NewStore(),
		signer:         signer,
		events:         NewEventLog(500),
		runs:           NewRunStore(),
		httpMiddleware: options.httpMiddleware,
		policy:         options.policy,
		audit:          options.audit,
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/scenarios", s.handleScenarios)
	s.mux.Handle("/api/run", s.protect(security.ActionRunSubmit, func(*http.Request) security.Resource { return security.Resource{Type: "run"} }, http.HandlerFunc(s.handleRun)))
	s.mux.Handle("/api/resume", s.protect(security.ActionHITLResume, func(*http.Request) security.Resource { return security.Resource{Type: "hitl"} }, http.HandlerFunc(s.handleResume)))
	s.mux.Handle("/api/runs/", s.protect(security.ActionRunRead, runResourceFromPath, http.HandlerFunc(s.handleRunDetail)))
	s.mux.Handle("/api/hitl/resume", s.protect(security.ActionHITLResume, func(*http.Request) security.Resource { return security.Resource{Type: "hitl"} }, humanhttp.NewLegacyHandler(humancli.NewGate(s.repo, s.signer, nil))))
}

func (s *Server) protect(action security.Action, resourceFunc securityhttp.ResourceFunc, handler http.Handler) http.Handler {
	wrapped := handler
	if s.policy != nil {
		middleware, err := securityhttp.NewMiddleware(securityhttp.MiddlewareConfig{Policy: s.policy, Action: action, ResourceFunc: resourceFunc, Audit: s.audit})
		if err != nil {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			})
		}
		wrapped = middleware(wrapped)
	}
	if s.httpMiddleware != nil {
		wrapped = s.httpMiddleware(wrapped)
	}
	return wrapped
}

func runResourceFromPath(r *http.Request) security.Resource {
	runID := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	return security.Resource{Type: "run", ID: runID}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleScenarios(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, builtinScenarios())
}

type runRequest struct {
	ScenarioID string          `json:"scenario_id"`
	YAML       string          `json:"yaml"`
	Prompt     string          `json:"prompt"`
	Context    json.RawMessage `json:"context"`
	Agent      string          `json:"agent"`
	RealModel  struct {
		Enabled bool   `json:"enabled"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		APIKey  string `json:"api_key"`
	} `json:"real_model"`
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	yml := req.YAML
	if strings.TrimSpace(yml) == "" {
		yml = builtinScenarioYAML(req.ScenarioID)
	}
	if strings.TrimSpace(yml) == "" {
		http.Error(w, "scenario yaml is required", http.StatusBadRequest)
		return
	}
	scenario, err := configyaml.Load([]byte(yml))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runID := fmt.Sprintf("debug-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	result, err := s.runScenario(ctx, scenario, runID, req)
	if err != nil {
		s.runs.Put(RunRecord{RunID: runID, ScenarioName: scenario.Name, Status: "failed", Error: err.Error(), UpdatedAt: time.Now().UTC()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	snapshot, _ := s.repo.Load(r.Context(), runID)
	record := RunRecord{
		RunID:        runID,
		ScenarioName: scenario.Name,
		Status:       string(result.Status),
		Token:        result.Token,
		Output:       result.Output,
		Snapshot:     snapshot,
		Events:       s.events.ByRun(runID),
		UpdatedAt:    time.Now().UTC(),
	}
	s.runs.Put(record)
	s.recordAudit(r.Context(), audit.Event{Type: audit.EventRunSubmitted, Principal: principalFromContext(r.Context()), Action: security.ActionRunSubmit, Resource: security.Resource{Type: "run", ID: runID}, RunID: runID, Outcome: string(result.Status)})
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) runScenario(ctx context.Context, scenario core.Scenario, runID string, req runRequest) (appexec.RunResult, error) {
	if scenario.Orchestration.Mode == core.OrchestrationFixedWorkflow {
		snapshot := runstate.RunSnapshot{RunID: runID, ScenarioName: scenario.Name, Status: runstate.RunStatusRunning, StepOutputs: make(map[string]runstate.StepOutputRef)}
		if err := s.repo.Save(ctx, &snapshot, 0); err != nil {
			return appexec.RunResult{}, err
		}
		reg := registry.New()
		for name := range scenario.Tools {
			if err := reg.RegisterTool(name, builtin.NewEchoTool()); err != nil {
				return appexec.RunResult{}, err
			}
		}
		gate := humancli.NewGate(s.repo, s.signer, nil)
		deps := appexec.Dependencies{Runs: s.repo, Blobs: s.blobs, Events: s.events, Tools: reg, HumanGate: gate}
		if req.RealModel.Enabled {
			if req.RealModel.BaseURL == "" || req.RealModel.Model == "" || req.RealModel.APIKey == "" {
				return appexec.RunResult{}, fmt.Errorf("real model base_url, model, and api_key are required")
			}
			profileName := firstProfileName(scenario)
			deps.LLM = llmrouter.New(map[string]llm.Gateway{
				profileName: openai.NewGateway([]llm.Profile{{
					Name:     profileName,
					Provider: "openai-compatible",
					Model:    req.RealModel.Model,
					Endpoint: strings.TrimRight(req.RealModel.BaseURL, "/"),
					Metadata: map[string]string{"api_key": req.RealModel.APIKey},
				}}, nil),
			})
		}
		engine, err := appexec.NewEngine(scenario, deps)
		if err != nil {
			return appexec.RunResult{}, err
		}
		runner := orchestration.NewWorkflowRunner(
			reg,
			s.repo,
			s.events,
			orchestration.WithAgentRegistry(runtimeAgentRegistry{agents: scenario.Agents, engine: engine}),
			orchestration.WithHumanGate(gate),
			orchestration.WithBlobStore(s.blobs),
		)
		if err := runner.Run(ctx, scenario, runID); err != nil {
			var paused orchestration.WorkflowPausedError
			if errors.As(err, &paused) {
				return appexec.RunResult{RunID: runID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
			}
			return appexec.RunResult{}, err
		}
		loaded, err := s.repo.Load(ctx, runID)
		if err != nil {
			return appexec.RunResult{}, err
		}
		loaded.Status = runstate.RunStatusCompleted
		if err := s.repo.Save(ctx, &loaded, loaded.Version); err != nil {
			return appexec.RunResult{}, err
		}
		_ = s.events.Emit(ctx, core.Event{Type: core.EventRunCompleted, RunID: runID, ScenarioName: scenario.Name, Timestamp: time.Now().UTC()})
		return appexec.RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: "fixed workflow completed"}, nil
	}

	deps := appexec.Dependencies{
		Runs:   s.repo,
		Blobs:  s.blobs,
		Events: s.events,
	}
	if req.RealModel.Enabled {
		if req.RealModel.BaseURL == "" || req.RealModel.Model == "" || req.RealModel.APIKey == "" {
			return appexec.RunResult{}, fmt.Errorf("real model base_url, model, and api_key are required")
		}
		profileName := firstProfileName(scenario)
		deps.LLM = llmrouter.New(map[string]llm.Gateway{
			profileName: openai.NewGateway([]llm.Profile{{
				Name:     profileName,
				Provider: "openai-compatible",
				Model:    req.RealModel.Model,
				Endpoint: strings.TrimRight(req.RealModel.BaseURL, "/"),
				Metadata: map[string]string{"api_key": req.RealModel.APIKey},
			}}, nil),
		})
	}
	if scenario.Orchestration.HumanInLoop.Enabled {
		deps.HumanGate = humancli.NewGate(s.repo, s.signer, nil)
	}
	engine, err := appexec.NewEngine(scenario, deps)
	if err != nil {
		return appexec.RunResult{}, err
	}
	return engine.Run(ctx, appexec.RunRequest{RunID: runID, Agent: req.Agent, Prompt: req.Prompt, Context: req.Context})
}

type resumeRequest struct {
	Token     string          `json:"token"`
	Decision  core.Decision   `json:"decision"`
	Amendment json.RawMessage `json:"amendment,omitempty"`
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req resumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	gate := humancli.NewGate(s.repo, s.signer, nil)
	if err := gate.Resume(r.Context(), req.Token, req.Decision, req.Amendment); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	payload, _ := s.signer.Verify(req.Token)
	snapshot, _ := s.repo.Load(r.Context(), payload.RunID)
	_ = s.events.Emit(r.Context(), core.Event{Type: core.EventHumanGateDecided, RunID: payload.RunID, ScenarioName: snapshot.ScenarioName, Timestamp: time.Now().UTC()})
	record := s.runs.Get(payload.RunID)
	record.RunID = payload.RunID
	record.ScenarioName = snapshot.ScenarioName
	record.Status = string(snapshot.Status)
	record.Snapshot = snapshot
	record.Events = s.events.ByRun(payload.RunID)
	record.UpdatedAt = time.Now().UTC()
	s.runs.Put(record)
	s.recordAudit(r.Context(), audit.Event{Type: audit.EventHITLDecided, Principal: principalFromContext(r.Context()), Action: security.ActionHITLResume, Resource: security.Resource{Type: "hitl", ID: payload.RunID}, RunID: payload.RunID, Outcome: string(req.Decision)})
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) recordAudit(ctx context.Context, event audit.Event) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, event.WithDefaults(time.Now().UTC()))
}

func principalFromContext(ctx context.Context) identity.Principal {
	principal, _ := identity.PrincipalFromContext(ctx)
	return principal
}

func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if runID == "" {
		http.NotFound(w, r)
		return
	}
	record := s.runs.Get(runID)
	if record.RunID == "" {
		snapshot, err := s.repo.Load(r.Context(), runID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		record = RunRecord{RunID: runID, ScenarioName: snapshot.ScenarioName, Status: string(snapshot.Status), Snapshot: snapshot}
	}
	record.Events = s.events.ByRun(runID)
	writeJSON(w, http.StatusOK, record)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func firstProfileName(scenario core.Scenario) string {
	for name := range scenario.LLMs {
		return name
	}
	return "default"
}

type RunRecord struct {
	RunID        string               `json:"run_id"`
	ScenarioName string               `json:"scenario_name,omitempty"`
	Status       string               `json:"status,omitempty"`
	Token        string               `json:"token,omitempty"`
	Output       string               `json:"output,omitempty"`
	Error        string               `json:"error,omitempty"`
	Snapshot     runstate.RunSnapshot `json:"snapshot,omitempty"`
	Events       []core.Event         `json:"events,omitempty"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

type RunStore struct {
	mu   sync.RWMutex
	runs map[string]RunRecord
}

func NewRunStore() *RunStore {
	return &RunStore{runs: make(map[string]RunRecord)}
}

func (s *RunStore) Put(record RunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[record.RunID] = record
}

func (s *RunStore) Get(runID string) RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runs[runID]
}

type EventLog struct {
	mu     sync.RWMutex
	limit  int
	events []core.Event
}

func NewEventLog(limit int) *EventLog {
	return &EventLog{limit: limit}
}

func (l *EventLog) Emit(ctx context.Context, event core.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
	if l.limit > 0 && len(l.events) > l.limit {
		l.events = l.events[len(l.events)-l.limit:]
	}
	return nil
}

func (l *EventLog) ByRun(runID string) []core.Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]core.Event, 0)
	for _, event := range l.events {
		if event.RunID == runID {
			out = append(out, event)
		}
	}
	return out
}
