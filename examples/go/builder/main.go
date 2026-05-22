package main

import (
	"context"
	"fmt"
	"log"

	examplescenario "github.com/aijustin/agentflow-go/examples/go/scenario"
	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenario := examplescenario.AutonomousEcho()
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: examplescenario.WorkDir})
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
