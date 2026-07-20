package agentflow

import (
	"context"
	"sync/atomic"

	"github.com/aijustin/agentflow-go/pkg/log"
)

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
