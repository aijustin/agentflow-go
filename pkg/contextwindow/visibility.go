package contextwindow

// Dual-visibility projection convention. A message's Metadata entry under
// MetadataKeyVisibility declares which audience may see it:
//
//	""      (absent) — visible to both the model ("agent") and the user
//	"agent" — visible to the model only
//	"user"  — visible to the user only; provider gateways must exclude it
//	          from the model-visible projection while events, memory, and
//	          checkpoints retain the full sequence
//
// The constants live in this package (not pkg/llm) because the Manager is
// what writes the marks when MarkInsteadOfDrop is enabled, and pkg/llm
// already imports pkg/contextwindow — the reverse would be a cycle.
const MetadataKeyVisibility = "visibility"

const (
	// VisibilityBoth is the zero value: no projection filtering.
	VisibilityBoth = ""
	// VisibilityAgentOnly marks a message the user projection must hide.
	VisibilityAgentOnly = "agent"
	// VisibilityUserOnly marks a message the model projection must hide.
	VisibilityUserOnly = "user"
)
