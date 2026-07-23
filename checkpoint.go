package agentflow

import (
	"fmt"
	"net/http"

	checkpointhttp "github.com/aijustin/agentflow-go/internal/adapter/checkpoint/http"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type CheckpointHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
	// Policy authorizes requests: reads as run.read, writes (resume-from-step,
	// resume-from-checkpoint, fork) as hitl.resume / run.submit, with the
	// caller's tenant bound to the resource. When Policy is nil the write
	// endpoints default-deny with 403 auth_required — mounting them open was
	// a privilege-escalation hole (any caller could resume or fork any run).
	Policy security.Policy
	// Audit receives policy-denied records when configured.
	Audit audit.Sink
	// InsecureAllowNoAuth disables the default-deny protection on write
	// endpoints when Policy is nil. Only set it behind an authenticating
	// reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

// NewCheckpointHTTPHandler serves production checkpoint routes:
//   - GET  /v1/runs/{run_id}/steps
//   - POST /v1/runs/{run_id}/resume-from-step
//   - GET  /v1/runs/{run_id}/checkpoints
//   - GET  /v1/runs/{run_id}/checkpoints/{version}
//   - POST /v1/runs/{run_id}/resume-from-checkpoint
//   - POST /v1/runs/{run_id}/fork
//
// Write routes require CheckpointHTTPHandlerConfig.Policy (or an explicit
// InsecureAllowNoAuth opt-out) since they mint fresh execution for a run.
func NewCheckpointHTTPHandler(config CheckpointHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: checkpoint handler requires framework")
	}
	adapter := &studioFramework{framework: config.Framework}
	return checkpointhttp.NewHandler(checkpointhttp.HandlerConfig{
		Checkpoint:          adapter,
		Steps:               adapter,
		History:             adapter,
		Checkpoints:         adapter,
		Restore:             adapter,
		Fork:                adapter,
		MaxBodyBytes:        config.MaxBodyBytes,
		Policy:              config.Policy,
		Audit:               config.Audit,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
	}), nil
}
