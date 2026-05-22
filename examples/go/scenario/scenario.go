// Package scenario provides builder stacks shared by examples/go programs.
package scenario

import (
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// WorkDir is the repository root when running from examples/go/*/main.go.
const WorkDir = "../.."

// AutonomousEcho matches catalog ID autonomous-echo.
func AutonomousEcho() core.Scenario {
	return builder.MinimalAutonomous("assistant",
		builder.MinimalScenarioName("autonomous-echo"),
		builder.MinimalInstructions("Answer the user clearly."),
	)
}

// HumanInLoop matches catalog ID human-in-loop.
func HumanInLoop() core.Scenario {
	return builder.MinimalHumanInLoop("assistant")
}

// TicketHandling matches catalog ID ticket-handling.
func TicketHandling() core.Scenario {
	return builder.MinimalTicketHandling("support")
}

// TierMemory matches catalog ID tier-memory.
func TierMemory() core.Scenario {
	return builder.TierMemoryAutonomous("assistant")
}
