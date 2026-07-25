package builder

import "github.com/aijustin/agentflow-go/pkg/core"

// MinimalGraphComposer builds a base "parts box" scenario for AI graph
// composition via Studio.ComposeGraph: the standard mock/session stack, an
// echo tool, one echo-capable agent, and a single-node fixed workflow that
// the composer can replace with a composed topology. Compose runs are
// ephemeral — this base stays untouched unless the host explicitly persists.
func MinimalGraphComposer(agentName string, opts ...MinimalOption) core.Scenario {
	cfg := defaultMinimalConfig(agentName)
	cfg.scenarioName = "compose-base"
	cfg.instructions = "You are a helpful assistant. Use the echo tool to repeat input when asked."
	for _, opt := range opts {
		opt(&cfg)
	}
	b := New(cfg.scenarioName).StandardStack()
	b.EchoTool()
	ab := b.Agent(agentName).
		StandardAgent().
		EchoTool().
		Instructions(cfg.instructions)
	wf := NewWorkflow().NodeAgent("assist", agentName).Build()
	return ab.FixedWorkflow(wf).Scenario()
}
