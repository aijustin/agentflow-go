package main

import (
	"context"
	"fmt"
	"log"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/testutil"
)

func main() {
	scenarioFile := "../../tier_memory.yaml"
	scenario, err := agentflow.LoadScenarioFile(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := testutil.ScenarioWorkDir(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	opts, err := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
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
		RunID:  "run-tier-1",
		Agent:  "assistant",
		Prompt: "remember this conversation uses tiered memory",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("status=%s output=%s\n", result.Status, result.Output)
}
