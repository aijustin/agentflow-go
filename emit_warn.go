package agentflow

import (
	"context"
	"sync/atomic"

	"github.com/aijustin/agentflow-go/pkg/core"
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
