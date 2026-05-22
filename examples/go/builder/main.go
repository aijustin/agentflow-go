package main

import (
	"context"
	"fmt"
	"log"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	// One-liner for the common autonomous-echo stack:
	scenario := builder.MinimalAutonomous("assistant",
		builder.MinimalScenarioName("autonomous-echo"),
		builder.MinimalInstructions("Answer the user clearly."),
	)

	if err := agentflow.ValidateScenario(scenario); err != nil {
		log.Fatal(err)
	}

	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: "../../"})
	if err != nil {
		log.Fatal(err)
	}
	if err := agentflow.ValidateWiring(scenario, opts...); err != nil {
		log.Fatal(err)
	}
	fw, err := agentflow.New(scenario, opts...)
	if err != nil {
		log.Fatal(err)
	}
	defer fw.Close(context.Background())

	result, err := fw.Run(context.Background(), agentflow.RunRequest{
		RunID:  "run-builder-1",
		Agent:  "assistant",
		Prompt: "hello from builder",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("status=%s output=%s\n", result.Status, result.Output)
}
