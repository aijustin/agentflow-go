package studio

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrorPayload is the structured Studio API error body.
type ErrorPayload struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Params  map[string]string `json:"params,omitempty"`
}

// CodedError is a Studio error with a stable machine-readable code.
type CodedError struct {
	Code    string
	Message string
	Params  map[string]string
}

func (e *CodedError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var (
	ErrSavePathMissing    = &CodedError{Code: "studio.save_path_missing", Message: "studio save path is not configured"}
	ErrGraphRequired      = &CodedError{Code: "studio.graph_required", Message: "graph is required"}
	ErrRunStateMissing    = &CodedError{Code: "studio.run_state_missing", Message: "run-state repository is not configured"}
	ErrCheckpointMissing  = &CodedError{Code: "studio.checkpoint_missing", Message: "checkpoint history is not configured"}
	ErrCompareRunsMissing = &CodedError{Code: "studio.compare_runs_missing", Message: "compare requires run_a and run_b"}
	ErrUnsupportedMode    = &CodedError{Code: "studio.unsupported_mode", Message: "studio run supports fixed_workflow and hybrid scenarios"}
	ErrNodeIDRequired     = &CodedError{Code: "obs.node_id_required", Message: "node_id is required"}
	ErrVersionRequired    = &CodedError{Code: "obs.version_required", Message: "version must be a positive integer"}
	ErrStreamingUnsupported = &CodedError{Code: "obs.streaming_unsupported", Message: "streaming is not supported"}
)

var duplicateNodePattern = regexp.MustCompile(`duplicate workflow node "([^"]+)"`)

// ErrorPayloadFrom maps an error to a structured Studio API payload.
func ErrorPayloadFrom(err error) ErrorPayload {
	if err == nil {
		return ErrorPayload{Code: "studio.internal", Message: "unknown error"}
	}
	var coded *CodedError
	if errors.As(err, &coded) && coded != nil {
		return ErrorPayload{Code: coded.Code, Message: coded.Message, Params: coded.Params}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "studio save path is required"), strings.Contains(msg, "studio save path is not configured"):
		return payloadFrom(ErrSavePathMissing, msg)
	case strings.Contains(msg, "graph is required"):
		return payloadFrom(ErrGraphRequired, msg)
	case strings.Contains(msg, "run-state repository is not configured"):
		return payloadFrom(ErrRunStateMissing, msg)
	case strings.Contains(msg, "checkpoint history is not configured"):
		return payloadFrom(ErrCheckpointMissing, msg)
	case strings.Contains(msg, "compare requires run_a and run_b"):
		return payloadFrom(ErrCompareRunsMissing, msg)
	case strings.Contains(msg, "studio run supports fixed_workflow and hybrid"):
		return payloadFrom(ErrUnsupportedMode, msg)
	case strings.Contains(msg, "duplicate workflow node"):
		if match := duplicateNodePattern.FindStringSubmatch(msg); len(match) == 2 {
			return ErrorPayload{Code: "graph.duplicate_node", Message: msg, Params: map[string]string{"id": match[1]}}
		}
		return ErrorPayload{Code: "graph.duplicate_node", Message: msg}
	case strings.HasPrefix(msg, "graph:"):
		return ErrorPayload{Code: "graph.invalid", Message: msg}
	case strings.Contains(msg, "decode body"), strings.Contains(msg, "invalid character"):
		return ErrorPayload{Code: "studio.invalid_json", Message: msg}
	case strings.Contains(msg, "node_id is required"):
		return payloadFrom(ErrNodeIDRequired, msg)
	case strings.Contains(msg, "version must be a positive integer"):
		return payloadFrom(ErrVersionRequired, msg)
	case strings.Contains(msg, "run_a and run_b are required"):
		return payloadFrom(ErrCompareRunsMissing, msg)
	case strings.Contains(msg, "streaming is not supported"):
		return payloadFrom(ErrStreamingUnsupported, msg)
	case strings.Contains(msg, " is not configured"):
		return ErrorPayload{Code: "obs.not_configured", Message: msg, Params: map[string]string{"feature": notConfiguredFeature(msg)}}
	default:
		return ErrorPayload{Code: "studio.internal", Message: msg}
	}
}

func notConfiguredFeature(msg string) string {
	switch {
	case strings.Contains(msg, "graph export"):
		return "graph_export"
	case strings.Contains(msg, "run steps"):
		return "run_steps"
	case strings.Contains(msg, "resume-from-step"):
		return "resume_from_step"
	case strings.Contains(msg, "checkpoint history"):
		return "checkpoint_history"
	case strings.Contains(msg, "checkpoint loading"):
		return "checkpoint_loading"
	case strings.Contains(msg, "resume-from-checkpoint"):
		return "resume_from_checkpoint"
	case strings.Contains(msg, "run compare"):
		return "run_compare"
	case strings.Contains(msg, "studio validate"):
		return "studio_validate"
	case strings.Contains(msg, "studio codegen"):
		return "studio_codegen"
	case strings.Contains(msg, "studio yaml"):
		return "studio_yaml"
	case strings.Contains(msg, "studio run"):
		return "studio_run"
	case strings.Contains(msg, "studio save"):
		return "studio_save"
	case strings.Contains(msg, "run thread"):
		return "run_thread"
	case strings.Contains(msg, "run fork"):
		return "run_fork"
	default:
		return "feature"
	}
}

func payloadFrom(base *CodedError, msg string) ErrorPayload {
	if strings.TrimSpace(msg) == "" {
		return ErrorPayload{Code: base.Code, Message: base.Message, Params: base.Params}
	}
	return ErrorPayload{Code: base.Code, Message: msg, Params: base.Params}
}

// WrapGraphError converts graph import errors into coded errors when possible.
func WrapGraphError(err error) error {
	if err == nil {
		return nil
	}
	payload := ErrorPayloadFrom(err)
	if payload.Code == "studio.internal" {
		return err
	}
	return &CodedError{Code: payload.Code, Message: payload.Message, Params: payload.Params}
}

// FormatMessage renders a developer-facing message for logs.
func FormatMessage(payload ErrorPayload) string {
	if len(payload.Params) == 0 {
		return payload.Message
	}
	return fmt.Sprintf("%s (%s)", payload.Message, payload.Code)
}
