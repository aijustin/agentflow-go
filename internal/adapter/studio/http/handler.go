package studiohttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/studio"
)

const DefaultMaxBodyBytes = int64(1 << 20)

type Validator interface {
	ValidateStudioGraph(ctx context.Context, graph any) (any, error)
}

type CodeGenerator interface {
	GenerateStudioBuilderCode(ctx context.Context, graph any) (any, error)
}

type YAMLExporter interface {
	GenerateStudioScenarioYAML(ctx context.Context, graph any) (any, error)
}

type YAMLImporter interface {
	ImportStudioScenarioYAML(ctx context.Context, yaml []byte, layout any) (any, error)
}

type Runner interface {
	RunStudioGraph(ctx context.Context, graph any, req any) (any, error)
}

type Saver interface {
	SaveStudioGraph(ctx context.Context, graph any) (any, error)
}

// Composer runs AI graph composition (ComposeGraphRequest in,
// ComposeGraphResult out, both passed as any for decoupling).
type Composer interface {
	ComposeStudioGraph(ctx context.Context, req any) (any, error)
}

// PartsLister returns the live scenario's composable parts.
type PartsLister interface {
	ListStudioParts() any
}

type HandlerConfig struct {
	Validate     Validator
	Codegen      CodeGenerator
	YAML         YAMLExporter
	ImportYAML   YAMLImporter
	Run          Runner
	Save         Saver
	Compose      Composer
	Parts        PartsLister
	MaxBodyBytes int64
	// Policy authorizes the mutating endpoints: studio run as run.submit and
	// studio save as admin.configure, mirroring the checkpoint handler.
	// When Policy is nil those endpoints default-deny with 403 auth_required
	// unless InsecureAllowNoAuth explicitly opts out; the pure-transform
	// endpoints (validate/codegen/yaml/import-yaml) stay open.
	Policy security.Policy
	// Audit receives policy-denied records when configured.
	Audit audit.Sink
	// InsecureAllowNoAuth disables the default-deny protection on the
	// mutating endpoints when no Policy is configured. Only set it behind an
	// authenticating reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

type Handler struct {
	validate     Validator
	codegen      CodeGenerator
	yaml         YAMLExporter
	importYAML   YAMLImporter
	run          Runner
	save         Saver
	compose      Composer
	parts        PartsLister
	maxBodyBytes int64
	policy       security.Policy
	audit        audit.Sink
	insecure     bool
}

func NewHandler(config HandlerConfig) *Handler {
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &Handler{
		validate:     config.Validate,
		codegen:      config.Codegen,
		yaml:         config.YAML,
		importYAML:   config.ImportYAML,
		run:          config.Run,
		save:         config.Save,
		compose:      config.Compose,
		parts:        config.Parts,
		maxBodyBytes: maxBodyBytes,
		policy:       config.Policy,
		audit:        config.Audit,
		insecure:     config.InsecureAllowNoAuth,
	}
}

func (h *Handler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.Trim(r.URL.Path, "/")
	if !strings.HasPrefix(path, "v1/studio") {
		nethttp.NotFound(w, r)
		return
	}
	switch path {
	case "v1/studio/validate":
		h.handleValidate(w, r)
	case "v1/studio/codegen":
		h.handleCodegen(w, r)
	case "v1/studio/yaml":
		h.handleYAML(w, r)
	case "v1/studio/import-yaml":
		h.handleImportYAML(w, r)
	case "v1/studio/run":
		h.handleRun(w, r)
	case "v1/studio/save":
		h.handleSave(w, r)
	case "v1/studio/compose":
		h.handleCompose(w, r)
	case "v1/studio/parts":
		h.handleParts(w, r)
	default:
		nethttp.NotFound(w, r)
	}
}

func (h *Handler) handleValidate(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.validate == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio validate is not configured")
		return
	}
	graph, err := decodeBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := h.validate.ValidateStudioGraph(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleCodegen(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.codegen == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio codegen is not configured")
		return
	}
	graph, err := decodeBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := h.codegen.GenerateStudioBuilderCode(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleYAML(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.yaml == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio yaml export is not configured")
		return
	}
	graph, err := decodeBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := h.yaml.GenerateStudioScenarioYAML(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleImportYAML(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if h.importYAML == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio yaml import is not configured")
		return
	}
	body, err := readBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	var payload struct {
		YAML        string `json:"yaml"`
		LayoutGraph any    `json:"layout_graph"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(payload.YAML) == "" {
		writeError(w, nethttp.StatusBadRequest, "yaml is required")
		return
	}
	result, err := h.importYAML.ImportStudioScenarioYAML(r.Context(), []byte(payload.YAML), payload.LayoutGraph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleRun(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if !h.requireWriteAuth(w, r, security.ActionRunSubmit, "run") {
		return
	}
	if h.run == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio run is not configured")
		return
	}
	body, err := readBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	var payload struct {
		Graph  any    `json:"graph"`
		Prompt string `json:"prompt"`
		Agent  string `json:"agent"`
		RunID  string `json:"run_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	if payload.Graph == nil {
		writeError(w, nethttp.StatusBadRequest, "graph is required")
		return
	}
	req := map[string]any{
		"prompt": strings.TrimSpace(payload.Prompt),
		"agent":  strings.TrimSpace(payload.Agent),
		"run_id": strings.TrimSpace(payload.RunID),
	}
	result, err := h.run.RunStudioGraph(r.Context(), payload.Graph, req)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleSave(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if !h.requireWriteAuth(w, r, security.ActionAdminConfig, "save") {
		return
	}
	if h.save == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio save is not configured")
		return
	}
	graph, err := decodeBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	result, err := h.save.SaveStudioGraph(r.Context(), graph)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleCompose(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		methodNotAllowed(w, nethttp.MethodPost)
		return
	}
	if !h.requireWriteAuth(w, r, security.ActionRunSubmit, "compose") {
		return
	}
	if h.compose == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio compose is not configured")
		return
	}
	body, err := readBody(r, h.maxBodyBytes)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	var payload struct {
		Prompt      string          `json:"prompt"`
		Mode        string          `json:"mode"`
		ComposerLLM string          `json:"composer_llm"`
		MaxSteps    int             `json:"max_steps"`
		Run         bool            `json:"run"`
		RunRequest  json.RawMessage `json:"run_request"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(payload.Prompt) == "" {
		writeError(w, nethttp.StatusBadRequest, "prompt is required")
		return
	}
	req := map[string]any{
		"prompt":       strings.TrimSpace(payload.Prompt),
		"mode":         strings.TrimSpace(payload.Mode),
		"composer_llm": strings.TrimSpace(payload.ComposerLLM),
		"max_steps":    payload.MaxSteps,
		"run":          payload.Run,
	}
	if len(payload.RunRequest) > 0 {
		var runRequest any
		if err := json.Unmarshal(payload.RunRequest, &runRequest); err == nil {
			req["run_request"] = runRequest
		}
	}
	result, err := h.compose.ComposeStudioGraph(r.Context(), req)
	if err != nil {
		writeStudioError(w, nethttp.StatusBadRequest, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *Handler) handleParts(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		methodNotAllowed(w, nethttp.MethodGet)
		return
	}
	if h.parts == nil {
		writeError(w, nethttp.StatusNotImplemented, "studio parts listing is not configured")
		return
	}
	writeJSON(w, nethttp.StatusOK, h.parts.ListStudioParts())
}

func decodeBody(r *nethttp.Request, maxBodyBytes int64) (any, error) {
	body, err := readBody(r, maxBodyBytes)
	if err != nil {
		return nil, err
	}
	var graph any
	if err := json.Unmarshal(body, &graph); err != nil {
		return nil, err
	}
	return graph, nil
}

func readBody(r *nethttp.Request, maxBodyBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, io.ErrUnexpectedEOF
	}
	return body, nil
}

func methodNotAllowed(w nethttp.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
}

func writeJSON(w nethttp.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w nethttp.ResponseWriter, status int, message string) {
	writeStudioError(w, status, errors.New(message))
}

// requireWriteAuth enforces the mutating-endpoint authorization contract,
// mirroring the checkpoint handler: with a Policy configured the caller must
// pass it; without one, studio run/save default-deny unless
// InsecureAllowNoAuth was set explicitly.
func (h *Handler) requireWriteAuth(w nethttp.ResponseWriter, r *nethttp.Request, action security.Action, id string) bool {
	if h.policy == nil {
		if h.insecure {
			return true
		}
		writeJSON(w, nethttp.StatusForbidden, map[string]string{
			"error":      "studio mutating endpoints require an authorization policy; configure Policy or explicitly set InsecureAllowNoAuth to disable this protection",
			"error_code": "auth_required",
		})
		return false
	}
	principal, err := identity.RequirePrincipal(r.Context())
	if err != nil {
		h.recordDenied(r, principal, action, id, security.ErrUnauthenticated)
		writeJSON(w, nethttp.StatusUnauthorized, map[string]string{"error": "unauthorized", "error_code": "unauthenticated"})
		return false
	}
	resource := security.BindTenant(principal, security.Resource{Type: "studio", ID: id})
	if err := h.policy.Authorize(r.Context(), principal, action, resource); err != nil {
		h.recordDenied(r, principal, action, id, err)
		status := nethttp.StatusForbidden
		code := "forbidden"
		if errors.Is(err, security.ErrUnauthenticated) {
			status = nethttp.StatusUnauthorized
			code = "unauthenticated"
		}
		writeJSON(w, status, map[string]string{"error": "forbidden", "error_code": code})
		return false
	}
	return true
}

func (h *Handler) recordDenied(r *nethttp.Request, principal identity.Principal, action security.Action, id string, reason error) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Record(r.Context(), audit.Event{Type: audit.EventPolicyDenied, Principal: principal, Action: action, Resource: security.Resource{Type: "studio", ID: id}, Outcome: "denied", Reason: reason.Error()})
}

func writeStudioError(w nethttp.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": studio.ErrorPayloadFrom(err)})
}
