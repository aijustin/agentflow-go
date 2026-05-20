package main

import (
	"io"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
)

func newFrameworkFromFlags(file, stateDir, tokenSecret string, tokenTTL time.Duration, tokenWriter io.Writer) (*agentflow.Framework, error) {
	scenario, err := agentflow.LoadScenarioFile(file)
	if err != nil {
		return nil, err
	}
	workDir, err := agentflow.DemoWorkDir(file)
	if err != nil {
		return nil, err
	}
	opts := []agentflow.Option{
		agentflow.WithHITLTokenSecret([]byte(tokenSecret), tokenWriter),
		agentflow.WithHITLTokenTTL(tokenTTL),
	}
	if stateDir != "" {
		repo, blobs, err := newRunStores(stateDir)
		if err != nil {
			return nil, err
		}
		opts = append(opts, agentflow.WithRunStateRepository(repo), agentflow.WithBlobStore(blobs))
	}
	demoOpts, err := agentflow.DemoOptions(scenario, agentflow.DemoConfig{WorkDir: workDir})
	if err != nil {
		return nil, err
	}
	opts = append(opts, demoOpts...)
	return agentflow.New(scenario, opts...)
}
