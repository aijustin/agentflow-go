package agentflow

import (
	"fmt"
	"net/http"

	checkpointhttp "github.com/aijustin/agentflow-go/internal/adapter/checkpoint/http"
)

type CheckpointHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
}

// NewCheckpointHTTPHandler serves production checkpoint routes:
//   - GET  /v1/runs/{run_id}/steps
//   - POST /v1/runs/{run_id}/resume-from-step
//   - GET  /v1/runs/{run_id}/checkpoints
//   - GET  /v1/runs/{run_id}/checkpoints/{version}
//   - POST /v1/runs/{run_id}/resume-from-checkpoint
func NewCheckpointHTTPHandler(config CheckpointHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: checkpoint handler requires framework")
	}
	adapter := &studioFramework{framework: config.Framework}
	return checkpointhttp.NewHandler(checkpointhttp.HandlerConfig{
		Checkpoint:   adapter,
		Steps:        adapter,
		History:      adapter,
		Checkpoints:  adapter,
		Restore:      adapter,
		MaxBodyBytes: config.MaxBodyBytes,
	}), nil
}
