package main

import (
	"io"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
)

func newFrameworkFromFlags(file, stateDir, tokenSecret string, tokenTTL time.Duration, tokenWriter io.Writer) (*agentflow.Framework, error) {
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
	return agentflow.NewFromFile(file, opts...)
}
