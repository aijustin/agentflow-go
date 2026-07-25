package agentflow_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type detachedCancellationBlockingGateway struct {
	started chan struct{}
	stopped chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newDetachedCancellationBlockingGateway() *detachedCancellationBlockingGateway {
	return &detachedCancellationBlockingGateway{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (*detachedCancellationBlockingGateway) Supports(_ string, capability llm.Capability) bool {
	return capability == llm.CapChat || capability == llm.CapStream
}

func (*detachedCancellationBlockingGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("chat should not be called")
}

func (gateway *detachedCancellationBlockingGateway) StreamChat(ctx context.Context, _ string, _ llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	gateway.calls.Add(1)
	chunks := make(chan llm.ChatChunk)
	gateway.once.Do(func() { close(gateway.started) })
	go func() {
		defer close(chunks)
		select {
		case <-ctx.Done():
			close(gateway.stopped)
		case <-gateway.release:
		}
	}()
	return chunks, nil
}

type detachedCancellationStreamStarter func(context.Context, *agentflow.Framework, agentflow.RunRequest) (<-chan struct{}, error)

func assertDetachedSnapshotCancellationStopsBlockingLLM(t *testing.T, runID string, start detachedCancellationStreamStarter) {
	t.Helper()
	gateway := newDetachedCancellationBlockingGateway()
	defer close(gateway.release)

	fw, err := agentflow.New(
		core.Scenario{
			Name: "detached-snapshot-cancel-repro",
			LLMs: map[string]core.LLMProfileRef{
				"default": {Provider: "mock", Model: "blocking"},
			},
			Agents: map[string]core.Agent{
				"assistant": {Name: "assistant", LLM: "default"},
			},
		},
		agentflow.WithLLMGateway(gateway),
	)
	if err != nil {
		t.Fatal(err)
	}

	callerCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	drained, err := start(
		callerCtx,
		fw,
		agentflow.RunRequest{RunID: runID, Agent: "assistant", Prompt: "block"},
	)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-gateway.started:
	case <-time.After(time.Second):
		t.Fatal("blocking LLM did not start")
	}

	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), runID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusCancelled
	if err := fw.RunStateRepository().Save(context.Background(), &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}

	select {
	case <-gateway.stopped:
	case <-time.After(time.Second):
		persisted, loadErr := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), runID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		t.Fatalf("blocking LLM continued after persisted cancellation: snapshot=%s", persisted.Status)
	}

	if err := callerCtx.Err(); err != nil {
		t.Fatalf("explicit run cancellation unexpectedly cancelled the presentation context: %v", err)
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("stream did not close after persisted cancellation")
	}
	persisted, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != runstate.RunStatusCancelled {
		t.Fatalf("cancelled snapshot was overwritten: status=%s", persisted.Status)
	}
	if message := persisted.Variables[runstate.VarRunErrorMessage]; len(message) != 0 {
		t.Fatalf("explicit cancellation was misclassified as a failure: %s", message)
	}
	if got := gateway.calls.Load(); got != 1 {
		t.Fatalf("cancellation must not retry or fall back to another LLM call: calls=%d", got)
	}
}

func TestDetachedStreamSnapshotCancellationStopsBlockingLLM(t *testing.T) {
	assertDetachedSnapshotCancellationStopsBlockingLLM(t, "detached-stream-snapshot-cancel", func(
		ctx context.Context,
		fw *agentflow.Framework,
		req agentflow.RunRequest,
	) (<-chan struct{}, error) {
		frames, err := fw.StreamRun(ctx, req, agentflow.WithStreamDetached())
		if err != nil {
			return nil, err
		}
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for range frames {
			}
		}()
		return drained, nil
	})
}

func TestLowLevelDetachedStreamSnapshotCancellationStopsBlockingLLM(t *testing.T) {
	assertDetachedSnapshotCancellationStopsBlockingLLM(t, "low-level-detached-stream-snapshot-cancel", func(
		ctx context.Context,
		fw *agentflow.Framework,
		req agentflow.RunRequest,
	) (<-chan struct{}, error) {
		chunks, err := fw.Stream(agentflow.StreamDetached(ctx), req)
		if err != nil {
			return nil, err
		}
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for range chunks {
			}
		}()
		return drained, nil
	})
}
