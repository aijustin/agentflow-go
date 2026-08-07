package runstate

// Snapshot variable keys shared by the framework facade and the runtime engine.
const (
	VarResumePrompt      = "resume_prompt"
	VarResumeAgent       = "resume_agent"
	VarResumeTrustMode   = "resume_trust_mode"
	VarResumeEpisodeID   = "resume_episode_id"
	VarResumeTriggerKind = "resume_trigger_kind"
	VarResumeSessionID   = "resume_session_id"
	VarRunErrorMessage   = "run_error_message"
	VarExecutionPhase    = "execution_phase"
	VarCheckpointKind    = "checkpoint_kind"
	// VarRunLeaseOwner marks a run snapshot as lease-managed by stamping the
	// owning worker ID. MarkAbandonedRuns only reaps Running runs carrying
	// this marker, so runs executed by workers without lease coordination are
	// never mistaken for zombies.
	VarRunLeaseOwner = "run_lease_owner"
	// VarCheckpointPendingPause marks pause-checkpoint metadata whose approval
	// was never confirmed by the gate (the process crashed between the
	// checkpoint write and gate.Pause). Resume paths clear it once the gate
	// confirms an approval; a Running run still carrying it must not have its
	// checkpoint executed.
	VarCheckpointPendingPause = "checkpoint_pending_pause"
	// VarTerminalPersistFailed marks a Running run whose worker finished
	// executing but exhausted every retry persisting the terminal status
	// (failed/cancelled). The value is the intended target status. The
	// abandoned-run reaper and operator inspection use it to distinguish
	// "worker done, settle lost to persistent CAS conflicts" from a genuinely
	// live run.
	VarTerminalPersistFailed = "terminal_persist_failed"
)

// Hybrid / checkpoint phase values stored under VarExecutionPhase.
const (
	ExecutionPhaseWorkflow   = "workflow"
	ExecutionPhaseAutonomous = "autonomous"
)

// Checkpoint kind values stored under VarCheckpointKind.
const (
	CheckpointKindBeforeFinalAnswer = "before_final_answer"
	CheckpointKindToolApproval      = "tool_approval"
)
