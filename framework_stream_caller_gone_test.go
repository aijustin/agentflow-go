package agentflow_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// trickleStreamGateway produces an unbounded stream of chunks (ctx-aware), so
// a caller that stops consuming leaves the engine with chunks it cannot
// deliver — the leak shape DEFECT_REPORT D11 describes.
type trickleStreamGateway struct {
	started chan struct{}
	once    sync.Once
}

func newTrickleStreamGateway() *trickleStreamGateway {
	return &trickleStreamGateway{started: make(chan struct{})}
}

func (g *trickleStreamGateway) Supports(string, llm.Capability) bool { return true }

func (g *trickleStreamGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}, nil
}

func (g *trickleStreamGateway) StreamChat(ctx context.Context, _ string, _ llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	ch := make(chan llm.ChatChunk)
	go func() {
		defer close(ch)
		g.once.Do(func() { close(g.started) })
		for i := 0; ; i++ {
			select {
			case ch <- llm.ChatChunk{Content: fmt.Sprintf("chunk-%d", i)}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// DEFECT_REPORT D11: a Framework.Stream caller that neither drains the
// returned channel nor cancels ctx used to leak the forwarder, the engine
// goroutine and the lease renewer forever. With WithStreamCallerGoneTimeout
// the framework cancels execution once a chunk stays undeliverable for the
// configured window, so the run settles (cancelled) and the lease is freed.
func TestFrameworkStreamCallerGoneTimeoutSettlesAbandonedStream(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	gateway := newTrickleStreamGateway()
	fw, err := agentflow.New(
		streamScenarioForGateway(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
		agentflow.WithStreamCallerGoneTimeout(50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Note: context.Background() — the caller never cancels.
	chunks, err := fw.Stream(context.Background(), agentflow.RunRequest{RunID: "run-caller-gone", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	if first := <-chunks; first.Content == "" {
		t.Fatalf("expected a first chunk, got %+v", first)
	}
	// The caller walks away: no more reads, no cancellation. The engine keeps
	// producing chunks that can no longer be delivered, so the caller-gone
	// timeout must cancel the stream and settle the run.
	awaitRunStatus(t, fw, "run-caller-gone", runstate.RunStatusCancelled)
	leaseMustBeFreeEventually(t, locker, "run-caller-gone")
	// The forwarder must have exited, closing the returned channel (one
	// in-flight chunk may race the cancellation; drain until closed).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-chunks:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream channel must close after the caller-gone timeout cancelled the run")
		}
	}
}

// DEFECT_REPORT D11 constraint: detached streams are exempt from the
// caller-gone timeout — their contract is to keep executing (and forwarding,
// for late readers) after the caller goes away.
func TestFrameworkStreamCallerGoneTimeoutLeavesDetachedStreamAlone(t *testing.T) {
	gateway := newSlowStreamGateway()
	fw, err := agentflow.New(
		streamScenarioForGateway(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithStreamCallerGoneTimeout(500*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := fw.Stream(agentflow.StreamDetached(context.Background()), agentflow.RunRequest{RunID: "run-detached-exempt", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	if first := <-chunks; first.Content != "partial " {
		t.Fatalf("unexpected first chunk: %+v", first)
	}
	// The caller stops reading without cancelling; the detached run must still
	// run to completion once the gateway unblocks — the timeout must not
	// cancel it.
	close(gateway.release)
	awaitRunStatus(t, fw, "run-detached-exempt", runstate.RunStatusCompleted)
	// The terminal chunk must still be delivered to the (late) caller rather
	// than drained away by a caller-gone cancellation.
	select {
	case chunk, ok := <-chunks:
		if !ok || !chunk.Done {
			t.Fatalf("detached stream must keep forwarding to a late reader, got chunk=%+v ok=%v", chunk, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("detached stream must not be torn down by the caller-gone timeout")
	}
	for range chunks {
	}
}
