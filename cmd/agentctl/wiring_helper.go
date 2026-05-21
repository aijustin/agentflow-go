package main

import (
	"io"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func demoWiringOptions(file, tokenSecret string, tokenWriter io.Writer) ([]agentflow.Option, error) {
	scenario, err := agentflow.LoadScenarioFile(file)
	if err != nil {
		return nil, err
	}
	return demoWiringOptionsForScenario(scenario, file, tokenSecret, tokenWriter)
}

func demoWiringOptionsForScenario(scenario core.Scenario, file, tokenSecret string, tokenWriter io.Writer) ([]agentflow.Option, error) {
	workDir, err := agentflow.DemoWorkDir(file)
	if err != nil {
		return nil, err
	}
	opts := []agentflow.Option{
		agentflow.WithHITLTokenSecret([]byte(tokenSecret), tokenWriter),
	}
	demoOpts, err := agentflow.DemoOptions(scenario, agentflow.DemoConfig{WorkDir: workDir})
	if err != nil {
		return nil, err
	}
	return append(opts, demoOpts...), nil
}

func validateScenarioWiring(file, tokenSecret string) error {
	scenario, err := agentflow.LoadScenarioFile(file)
	if err != nil {
		return err
	}
	opts, err := demoWiringOptionsForScenario(scenario, file, tokenSecret, io.Discard)
	if err != nil {
		return err
	}
	return agentflow.ValidateWiring(scenario, opts...)
}
