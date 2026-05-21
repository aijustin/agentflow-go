package main

import (
	"io"
	"log/slog"
	"os"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
)

func newFrameworkFromFlags(file, stateDir, tokenSecret string, tokenTTL time.Duration, tokenWriter io.Writer, verbose bool, verboseWriter io.Writer) (*agentflow.Framework, error) {
	scenario, err := agentflow.LoadScenarioFile(file)
	if err != nil {
		return nil, err
	}
	opts, err := demoWiringOptions(file, tokenSecret, tokenWriter)
	if err != nil {
		return nil, err
	}
	opts = append(opts, agentflow.WithHITLTokenTTL(tokenTTL))
	if stateDir != "" {
		repo, blobs, err := newRunStores(stateDir)
		if err != nil {
			return nil, err
		}
		opts = append(opts, agentflow.WithRunStateRepository(repo), agentflow.WithBlobStore(blobs))
	}
	if verbose {
		if verboseWriter == nil {
			verboseWriter = os.Stderr
		}
		logger := slog.New(slog.NewTextHandler(verboseWriter, &slog.HandlerOptions{Level: slog.LevelInfo}))
		opts = append(opts, agentflow.WithEventSink(agentflow.NewVerboseSlogEventSink(logger)))
	}
	return agentflow.New(scenario, opts...)
}
