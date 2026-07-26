package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func streamServer(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func streamGateway(server *httptest.Server) *Gateway {
	return NewGateway([]llm.Profile{{
		Name:         "claude",
		Model:        "claude-sonnet",
		Endpoint:     server.URL,
		Capabilities: []llm.Capability{llm.CapChat, llm.CapToolCall, llm.CapStream},
	}}, server.Client())
}

func collect(t *testing.T, ch <-chan llm.ChatChunk) []llm.ChatChunk {
	t.Helper()
	var chunks []llm.ChatChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	return chunks
}

// A stream that ends cleanly without message_stop must still deliver a
// terminal chunk, otherwise a consumer waiting on Done blocks forever.
func TestStreamChatEmitsDoneOnCleanEOFWithoutMessageStop(t *testing.T) {
	server := streamServer(t,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	)
	ch, err := streamGateway(server).StreamChat(context.Background(), "claude", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := collect(t, ch)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Fatalf("expected a terminal Done chunk, got %+v", last)
	}
}

func TestStreamChatEmitsExactlyOneDone(t *testing.T) {
	server := streamServer(t,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	)
	ch, err := streamGateway(server).StreamChat(context.Background(), "claude", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	for _, chunk := range collect(t, ch) {
		if chunk.Done {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("expected exactly one Done chunk, got %d", done)
	}
}

// tool_use arguments arrive as input_json_delta fragments that must be
// reassembled into one tool call.
func TestStreamChatWithToolsAssemblesToolCallFromFragments(t *testing.T) {
	server := streamServer(t,
		`{"type":"message_start","message":{"usage":{"input_tokens":11}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"search"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"agents\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
	)
	ch, err := streamGateway(server).StreamChatWithTools(context.Background(), "claude", llm.ToolCallRequest{
		Tools: []llm.ToolSpec{{Name: "search"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := collect(t, ch)

	var calls []llm.ChatChunk
	for _, chunk := range chunks {
		if chunk.Kind == llm.ChunkKindToolCall {
			calls = append(calls, chunk)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call chunk, got %d (%+v)", len(calls), chunks)
	}
	if calls[0].ToolCallID != "toolu_1" || calls[0].ToolName != "search" {
		t.Fatalf("unexpected tool call identity %+v", calls[0])
	}
	var input map[string]string
	if err := json.Unmarshal(calls[0].ToolInput, &input); err != nil {
		t.Fatalf("tool input was not reassembled into valid JSON: %v (%s)", err, calls[0].ToolInput)
	}
	if input["query"] != "agents" {
		t.Fatalf("unexpected tool input %s", calls[0].ToolInput)
	}

	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Fatalf("expected Done last, got %+v", last)
	}
	// Input tokens come from message_start, output tokens from message_delta;
	// neither may overwrite the other.
	if last.Usage.InputTokens != 11 || last.Usage.OutputTokens != 5 || last.Usage.TotalTokens != 16 {
		t.Fatalf("unexpected usage %+v", last.Usage)
	}
}

// Multiple parallel tool calls must be emitted in content-block order.
func TestStreamChatWithToolsPreservesToolCallOrder(t *testing.T) {
	server := streamServer(t,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"first"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"second"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"message_stop"}`,
	)
	ch, err := streamGateway(server).StreamChatWithTools(context.Background(), "claude", llm.ToolCallRequest{
		Tools: []llm.ToolSpec{{Name: "first"}, {Name: "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, chunk := range collect(t, ch) {
		if chunk.Kind == llm.ChunkKindToolCall {
			names = append(names, chunk.ToolName)
		}
	}
	if strings.Join(names, ",") != "first,second" {
		t.Fatalf("expected tool calls in block order, got %v", names)
	}
}

// Text preceding a tool call is forwarded live for presentation.
func TestStreamChatWithToolsForwardsTextBeforeToolCall(t *testing.T) {
	server := streamServer(t,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me look."}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"search"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"message_stop"}`,
	)
	ch, err := streamGateway(server).StreamChatWithTools(context.Background(), "claude", llm.ToolCallRequest{
		Tools: []llm.ToolSpec{{Name: "search"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := collect(t, ch)
	var text strings.Builder
	for _, chunk := range chunks {
		text.WriteString(chunk.Content)
	}
	if text.String() != "Let me look." {
		t.Fatalf("expected preamble text to be streamed, got %q", text.String())
	}
}

// A mid-stream error event still has to terminate the channel with Done so the
// consumer can classify and retry rather than hang.
func TestStreamChatWithToolsTerminatesOnStreamError(t *testing.T) {
	server := streamServer(t,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"search"}}`,
		`{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
	)
	ch, err := streamGateway(server).StreamChatWithTools(context.Background(), "claude", llm.ToolCallRequest{
		Tools: []llm.ToolSpec{{Name: "search"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := collect(t, ch)
	last := chunks[len(chunks)-1]
	if !last.Done || last.Error == "" {
		t.Fatalf("expected a terminal error chunk, got %+v", last)
	}
	var apiErr llm.APIError
	if !errorAs(last.Err, &apiErr) {
		t.Fatalf("expected structured APIError for retry classification, got %T", last.Err)
	}
	if apiErr.StatusCode != 529 {
		t.Fatalf("expected overloaded_error mapped to 529, got %d", apiErr.StatusCode)
	}
	// A partial tool call must not be emitted alongside the failure.
	for _, chunk := range chunks {
		if chunk.Kind == llm.ChunkKindToolCall {
			t.Fatal("expected no tool call chunk on a failed stream")
		}
	}
}

// The gateway must satisfy the interface the router type-asserts against,
// otherwise tool-augmented streaming fails at runtime for Anthropic profiles.
func TestGatewayImplementsToolCallStreamer(t *testing.T) {
	var _ llm.ToolCallStreamer = (*Gateway)(nil)
}

func errorAs(err error, target *llm.APIError) bool {
	if err == nil {
		return false
	}
	apiErr, ok := err.(llm.APIError)
	if !ok {
		return false
	}
	*target = apiErr
	return true
}
