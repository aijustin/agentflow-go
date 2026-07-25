// Command compose-graph demonstrates AI graph composition (Studio.ComposeGraph)
// in both modes: catalog (orchestrate existing parts only) and scenario
// (compose new agents plus topology). A mock LLM gateway plays the role of
// the composer model; with a real provider the same calls run unchanged.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
)

// echoExecutor is a minimal executor for the scenario's echo tool.
type echoExecutor struct{}

func (echoExecutor) Execute(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: call.Tool, Output: call.Input}, nil
}

func main() {
	scenario := builder.MinimalGraphComposer("assistant")
	gateway := llmmock.NewFallbackGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)

	// --- catalog mode: the composer may only reuse registered parts.
	queueCall(gateway, "k1", "compose_list_parts", `{"kind":"tool"}`)
	queueCall(gateway, "k2", "compose_add_node", `{"id":"prepare","kind":"transform","input":{"set":{"topic":"release notes"}}}`)
	queueCall(gateway, "k3", "compose_add_node", `{"id":"write","kind":"agent","ref":"assistant"}`)
	queueCall(gateway, "k4", "compose_connect", `{"from":"prepare","to":"write"}`)
	queueCall(gateway, "k5", "compose_finish", `{}`)
	queueAnswer(gateway)

	// --- scenario mode: the composer creates a new agent, then runs the graph.
	queueCall(gateway, "s1", "compose_add_agent", `{"name":"reviewer","description":"reviews drafts","instructions":"Review the draft and list concrete improvements."}`)
	queueCall(gateway, "s2", "compose_add_node", `{"id":"draft","kind":"agent","ref":"assistant"}`)
	queueCall(gateway, "s3", "compose_add_node", `{"id":"review","kind":"agent","ref":"reviewer"}`)
	queueCall(gateway, "s4", "compose_connect", `{"from":"draft","to":"review"}`)
	queueCall(gateway, "s5", "compose_finish", `{}`)
	queueAnswer(gateway)

	fw, err := agentflow.New(scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", echoExecutor{}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	catalog, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt: "Prepare a topic and write about it.",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("catalog: valid=%v nodes=%d edges=%d\n", catalog.Valid, len(catalog.Graph.Workflow.Nodes), len(catalog.Graph.Workflow.Edges))

	composed, err := fw.ComposeGraph(context.Background(), agentflow.ComposeGraphRequest{
		Prompt:     "Draft a paragraph, then review it.",
		Mode:       agentflow.ComposeModeScenario,
		Run:        true,
		RunRequest: agentflow.RunRequest{RunID: "compose-demo-1"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("scenario: valid=%v agents=%v\n", composed.Valid, agentNames(composed.Scenario))
	if composed.Run != nil {
		fmt.Printf("scenario run: status=%s output=%.80s\n", composed.Run.Status, composed.Run.Output)
	}

	// The live scenario is untouched; persist explicitly if desired:
	//   fw.SaveStudioGraph(ctx, composed.Graph, "scenario.yaml")
	fmt.Printf("live workflow nodes=%d (unchanged)\n", len(fw.ExportScenarioGraph().Workflow.Nodes))
}

func queueCall(gateway *llmmock.FallbackGateway, id, tool, input string) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: id, Name: tool, Input: json.RawMessage(input)}},
	})
}

func queueAnswer(gateway *llmmock.FallbackGateway) {
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "graph ready"}},
	})
}

func agentNames(scenario *core.Scenario) []string {
	names := make([]string, 0, len(scenario.Agents))
	for name := range scenario.Agents {
		names = append(names, name)
	}
	return names
}
