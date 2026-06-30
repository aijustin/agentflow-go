package async

import "fmt"

// RunPausedError indicates a run job paused awaiting human approval.
// Workers should mark the job as paused rather than completed or failed.
type RunPausedError struct {
	RunID string
	Token string
}

func (e RunPausedError) Error() string {
	return fmt.Sprintf("async: run %q paused awaiting approval", e.RunID)
}

// PauseResult carries metadata persisted when a job pauses for HITL.
type PauseResult struct {
	RunID string `json:"run_id"`
	Token string `json:"token"`
}
