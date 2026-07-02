package memory

const (
	// ProvenanceKey is the metadata key used to record how a memory message
	// entered the repository.
	ProvenanceKey = "provenance"

	// ProvenanceRunTurn marks user/assistant turns written by the framework
	// during a normal run or stream completion.
	ProvenanceRunTurn = "run_turn"

	// ProvenanceToolLoop marks assistant tool-call turns and tool results
	// persisted by the framework tool loop.
	ProvenanceToolLoop = "tool_loop"

	// ProvenanceIntegrator marks messages seeded by an integrator before or
	// between framework runs (for example chat transcript hydration).
	ProvenanceIntegrator = "integrator"

	// ProvenanceUntracked marks messages with no provenance metadata. This
	// usually indicates data written before provenance tracking or by an
	// external writer that did not set metadata.provenance.
	ProvenanceUntracked = "untracked"
)
