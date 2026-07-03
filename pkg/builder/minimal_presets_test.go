package builder_test

import (
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestMinimalAutonomousVariants(t *testing.T) {
	echo := builder.MinimalAutonomous("assistant", builder.MinimalScenarioName("custom-echo"))
	if echo.Name != "custom-echo" || len(echo.Tools) == 0 {
		t.Fatalf("echo scenario: %+v", echo)
	}
	repo := builder.MinimalAutonomous("assistant", builder.MinimalRepoSearch(), builder.MinimalInstructions("search code"))
	if repo.Agents["assistant"].Instructions != "search code" {
		t.Fatalf("instructions=%q", repo.Agents["assistant"].Instructions)
	}
	if err := agentflow.ValidateScenario(repo); err != nil {
		t.Fatal(err)
	}
	echoOnly := builder.MinimalAutonomous("assistant", builder.MinimalEcho())
	if len(echoOnly.Tools) != 1 {
		t.Fatalf("expected single echo tool, got %+v", echoOnly.Tools)
	}
}

func TestNewMinimalAndMinimalAgent(t *testing.T) {
	scenario := builder.NewMinimal("minimal-stack").
		EchoTool().Done().
		MinimalAgent("assistant", "help", builder.NameEchoTool).
		Autonomous().
		Scenario()
	if len(scenario.Agents) != 1 || scenario.Agents["assistant"].Tools[0] != builder.NameEchoTool {
		t.Fatalf("unexpected scenario: %+v", scenario)
	}
}

func TestNamedWorkflowBuilder(t *testing.T) {
	wf := builder.NewWorkflow().NodeTransform("a", nil).Build()
	scenario := builder.New("named-wf").
		DefaultMockLLM().
		NamedWorkflow("prep", wf).
		NamedWorkflowBuilder("empty", nil).
		Agent("assistant").Done().
		Autonomous().
		Scenario()
	if len(scenario.Orchestration.Workflows) != 2 {
		t.Fatalf("workflows=%+v", scenario.Orchestration.Workflows)
	}
}

func TestScenarioBuilderMustScenarioPanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for invalid scenario")
		}
	}()
	builder.New("").MustScenario()
}

func TestStackPresetsBuildValidScenarios(t *testing.T) {
	agent := "assistant"
	cases := map[string]func() core.Scenario{
		"ticket-handling":       func() core.Scenario { return builder.MinimalTicketHandling(agent) },
		"rag-knowledge":         func() core.Scenario { return builder.MinimalRAG(agent) },
		"tier-memory":           func() core.Scenario { return builder.TierMemoryAutonomous(agent) },
		"adaptive-rag":          func() core.Scenario { return builder.AdaptiveRAG(agent) },
		"corrective-rag":        func() core.Scenario { return builder.CorrectiveRAG(agent) },
		"self-rag":              func() core.Scenario { return builder.SelfRAG(agent) },
		"hybrid-research":       func() core.Scenario { return builder.HybridResearch(agent) },
		"human-in-loop":         func() core.Scenario { return builder.MinimalHumanInLoop(agent) },
		"multi-expert-research": func() core.Scenario { return builder.MultiExpertResearch() },
		"code-review-pipeline":  func() core.Scenario { return builder.CodeReviewPipeline() },
		"workflow-enhancements": func() core.Scenario { return builder.WorkflowEnhancements() },
		"context-governance":    func() core.Scenario { return builder.ContextGovernance(agent) },
		"declarative-interrupt": func() core.Scenario { return builder.MinimalDeclarativeInterrupt() },
		"fixed-workflow-review": func() core.Scenario { return builder.MinimalFixedWorkflowReview(agent) },
		"http-tool":             func() core.Scenario { return builder.MinimalHTTPTool(agent) },
		"sql-tool":              func() core.Scenario { return builder.MinimalSQLTool(agent) },
		"filesystem-tool":       func() core.Scenario { return builder.MinimalFilesystemTool(agent) },
		"mcp-tool":              func() core.Scenario { return builder.MinimalMCPTool(agent) },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			scenario := build()
			if scenario.Name == "" {
				t.Fatal("expected scenario name")
			}
			if err := agentflow.ValidateScenario(scenario); err != nil {
				t.Fatal(err)
			}
		})
	}
}
