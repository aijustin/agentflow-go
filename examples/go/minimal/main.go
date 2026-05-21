package main

import (
	"context"
	"fmt"
	"log"

	agentflow "github.com/aijustin/agentflow-go"
)

func main() {
	scenarioFile := "../../autonomous.yaml"
	scenario, err := agentflow.LoadScenarioFile(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	workDir, err := agentflow.DemoWorkDir(scenarioFile)
	if err != nil {
		log.Fatal(err)
	}
	opts, err := agentflow.DevelopmentOptions(scenario, agentflow.DevelopmentConfig{WorkDir: workDir})
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
		RunID:  "minimal-run-1",
		Agent:  "assistant",
		Prompt: "hello from library embedder",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Output)
}
