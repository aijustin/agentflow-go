package studiohttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strings"

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

type HandlerConfig struct {
	Validate     Validator
	Codegen      CodeGenerator
	YAML         YAMLExporter
	ImportYAML   YAMLImporter
	Run          Runner
	Save         Saver
	MaxBodyBytes int64
}

type Handler struct {
	validate     Validator
	codegen      CodeGenerator
	yaml         YAMLExporter
	importYAML   YAMLImporter
	run          Runner
	save         Saver
	maxBodyBytes int64
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
		maxBodyBytes: maxBodyBytes,
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

func writeStudioError(w nethttp.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": studio.ErrorPayloadFrom(err)})
}
