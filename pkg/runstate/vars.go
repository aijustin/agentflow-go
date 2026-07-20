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
