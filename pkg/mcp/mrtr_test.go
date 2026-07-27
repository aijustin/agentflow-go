package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func modernOptions(elicitor Elicitor) ClientOptions {
	options, err := NormalizeClientOptions(ClientOptions{Mode: ProtocolModeModern, Elicitor: elicitor}, "test")
	if err != nil {
		panic(err)
	}
	return options
}

// recordingInvoker replays a scripted sequence of raw results and records the
// params it was called with, so a test can assert the retry payload.
type recordingInvoker struct {
	results []string
	calls   []map[string]any
}

func (r *recordingInvoker) invoke(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return nil, err
	}
	r.calls = append(r.calls, decoded)
	if len(r.calls) > len(r.results) {
		return nil, errors.New("invoker called more times than scripted")
	}
	return json.RawMessage(r.results[len(r.calls)-1]), nil
}

func TestCallToolWithInputReturnsOrdinaryResultUntouched(t *testing.T) {
	invoker := &recordingInvoker{results: []string{`{"content":[{"type":"text","text":"done"}]}`}}
	result, err := CallToolWithInput(context.Background(),
		CallToolRequest{Name: "search"}, modernOptions(nil), invoker.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "done" {
		t.Fatalf("unexpected result %+v", result)
	}
	if len(invoker.calls) != 1 {
		t.Fatalf("expected a single round trip, got %d", len(invoker.calls))
	}
}

// The retry has to carry the original arguments, the answers keyed the same
// way the server keyed the questions, and the opaque state echoed unchanged.
// Anything less and a different server instance cannot resume the work.
func TestCallToolWithInputRetriesWithAnswersAndEchoedState(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required",
		  "inputRequests":{"q1":{"method":"elicitation/create","params":{"message":"Which account?","requestedSchema":{"type":"object"}}}},
		  "requestState":{"cursor":"abc","attempt":1}}`,
		`{"content":[{"type":"text","text":"reconciled"}]}`,
	}}
	var seen ElicitRequest
	elicitor := ElicitorFunc(func(_ context.Context, req ElicitRequest) (ElicitResult, error) {
		seen = req
		return ElicitResult{Action: ElicitAccept, Content: json.RawMessage(`{"account":"ACME"}`)}, nil
	})

	result, err := CallToolWithInput(context.Background(),
		CallToolRequest{Name: "reconcile", Arguments: json.RawMessage(`{"month":"july"}`)},
		modernOptions(elicitor), invoker.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content[0].Text != "reconciled" {
		t.Fatalf("unexpected result %+v", result)
	}
	if len(invoker.calls) != 2 {
		t.Fatalf("expected two round trips, got %d", len(invoker.calls))
	}

	if seen.Key != "q1" || seen.Message != "Which account?" {
		t.Fatalf("elicitation not passed through: %+v", seen)
	}
	if seen.Mode != ElicitModeForm {
		t.Fatalf("expected form mode by default, got %q", seen.Mode)
	}

	retry := invoker.calls[1]
	if retry["name"] != "reconcile" {
		t.Fatalf("retry lost the tool name: %+v", retry)
	}
	args, _ := json.Marshal(retry["arguments"])
	if string(args) != `{"month":"july"}` {
		t.Fatalf("retry lost the original arguments: %s", args)
	}
	state, _ := json.Marshal(retry["requestState"])
	if string(state) != `{"attempt":1,"cursor":"abc"}` {
		t.Fatalf("requestState was not echoed unchanged: %s", state)
	}
	responses, ok := retry["inputResponses"].(map[string]any)
	if !ok {
		t.Fatalf("retry carried no inputResponses: %+v", retry)
	}
	answer, ok := responses["q1"].(map[string]any)
	if !ok {
		t.Fatalf("answer not keyed by the server's key: %+v", responses)
	}
	if answer["action"] != string(ElicitAccept) {
		t.Fatalf("unexpected action %+v", answer)
	}
}

// A decline is a valid answer, not an error: the server decides what to do
// with it.
func TestCallToolWithInputForwardsDecline(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required","inputRequests":{"q1":{"method":"elicitation/create","params":{"message":"Approve?"}}}}`,
		`{"content":[{"type":"text","text":"skipped"}],"isError":false}`,
	}}
	elicitor := ElicitorFunc(func(_ context.Context, _ ElicitRequest) (ElicitResult, error) {
		return ElicitResult{Action: ElicitDecline}, nil
	})
	result, err := CallToolWithInput(context.Background(),
		CallToolRequest{Name: "deploy"}, modernOptions(elicitor), invoker.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content[0].Text != "skipped" {
		t.Fatalf("unexpected result %+v", result)
	}
	responses := invoker.calls[1]["inputResponses"].(map[string]any)
	if responses["q1"].(map[string]any)["action"] != string(ElicitDecline) {
		t.Fatalf("expected the decline forwarded, got %+v", responses)
	}
}

// A result carrying only requestState is a bare retry signal.
func TestCallToolWithInputRetriesOnStateOnlyResult(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required","requestState":{"stage":2}}`,
		`{"content":[{"type":"text","text":"done"}]}`,
	}}
	if _, err := CallToolWithInput(context.Background(),
		CallToolRequest{Name: "slow"}, modernOptions(nil), invoker.invoke); err != nil {
		t.Fatal(err)
	}
	if len(invoker.calls) != 2 {
		t.Fatalf("expected an immediate retry, got %d calls", len(invoker.calls))
	}
	if _, ok := invoker.calls[1]["inputResponses"]; ok {
		t.Fatal("expected no inputResponses when nothing was asked")
	}
}

// Without an Elicitor the client never declares the capability, so a server
// asking anyway is out of contract and must fail rather than hang or guess.
func TestCallToolWithInputFailsClosedWithoutElicitor(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required","inputRequests":{"q1":{"method":"elicitation/create","params":{"message":"?"}}}}`,
	}}
	_, err := CallToolWithInput(context.Background(),
		CallToolRequest{Name: "search"}, modernOptions(nil), invoker.invoke)
	if err == nil {
		t.Fatal("expected an error when no Elicitor is configured")
	}
	if !strings.Contains(err.Error(), "no Elicitor") {
		t.Fatalf("unexpected error %v", err)
	}
}

// Sampling and roots are deprecated in the revision that introduced MRTR and
// are never advertised, so a request for them is out of contract.
func TestCallToolWithInputRejectsDeprecatedRequestKinds(t *testing.T) {
	for _, method := range []string{MethodSamplingCreateMessage, MethodRootsList} {
		invoker := &recordingInvoker{results: []string{
			`{"resultType":"input_required","inputRequests":{"q1":{"method":"` + method + `","params":{}}}}`,
		}}
		elicitor := ElicitorFunc(func(context.Context, ElicitRequest) (ElicitResult, error) {
			return ElicitResult{Action: ElicitAccept}, nil
		})
		_, err := CallToolWithInput(context.Background(),
			CallToolRequest{Name: "x"}, modernOptions(elicitor), invoker.invoke)
		if err == nil || !strings.Contains(err.Error(), "does not support") {
			t.Fatalf("expected %s to be rejected, got %v", method, err)
		}
	}
}

// A server that keeps asking must not pin the caller forever.
func TestCallToolWithInputBoundsInputRounds(t *testing.T) {
	forever := `{"resultType":"input_required","inputRequests":{"q":{"method":"elicitation/create","params":{"message":"again?"}}}}`
	results := make([]string, 20)
	for i := range results {
		results[i] = forever
	}
	invoker := &recordingInvoker{results: results}
	options := modernOptions(ElicitorFunc(func(context.Context, ElicitRequest) (ElicitResult, error) {
		return ElicitResult{Action: ElicitAccept}, nil
	}))
	options.MaxInputRounds = 3

	_, err := CallToolWithInput(context.Background(), CallToolRequest{Name: "loop"}, options, invoker.invoke)
	if err == nil || !strings.Contains(err.Error(), "input rounds") {
		t.Fatalf("expected a round limit error, got %v", err)
	}
	if len(invoker.calls) != 3 {
		t.Fatalf("expected exactly 3 round trips, got %d", len(invoker.calls))
	}
}

// MRTR only exists at 2026-07-28; a legacy-mode client must say so plainly
// rather than send fields the server will not understand.
func TestCallToolWithInputRejectsInputRequiredInLegacyMode(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required","inputRequests":{"q1":{"method":"elicitation/create","params":{}}}}`,
	}}
	options, err := NormalizeClientOptions(ClientOptions{Mode: ProtocolModeLegacy}, "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = CallToolWithInput(context.Background(), CallToolRequest{Name: "x"}, options, invoker.invoke)
	if err == nil || !strings.Contains(err.Error(), ProtocolVersionModern) {
		t.Fatalf("expected a protocol-version error, got %v", err)
	}
}

func TestCallToolWithInputPropagatesElicitorError(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required","inputRequests":{"q1":{"method":"elicitation/create","params":{}}}}`,
	}}
	sentinel := errors.New("user gate closed")
	options := modernOptions(ElicitorFunc(func(context.Context, ElicitRequest) (ElicitResult, error) {
		return ElicitResult{}, sentinel
	}))
	_, err := CallToolWithInput(context.Background(), CallToolRequest{Name: "x"}, options, invoker.invoke)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the elicitor error to propagate, got %v", err)
	}
}

// URL mode is what servers must use for credentials, so the mode has to reach
// the host rather than being flattened into a generic prompt.
func TestCallToolWithInputSurfacesURLMode(t *testing.T) {
	invoker := &recordingInvoker{results: []string{
		`{"resultType":"input_required","inputRequests":{"login":{"method":"elicitation/create","params":{"mode":"url","url":"https://example.test/auth","message":"Sign in"}}}}`,
		`{"content":[{"type":"text","text":"ok"}]}`,
	}}
	var seen ElicitRequest
	options := modernOptions(ElicitorFunc(func(_ context.Context, req ElicitRequest) (ElicitResult, error) {
		seen = req
		return ElicitResult{Action: ElicitAccept}, nil
	}))
	if _, err := CallToolWithInput(context.Background(), CallToolRequest{Name: "x"}, options, invoker.invoke); err != nil {
		t.Fatal(err)
	}
	if seen.Mode != ElicitModeURL || seen.URL != "https://example.test/auth" {
		t.Fatalf("url-mode elicitation not surfaced: %+v", seen)
	}
}

func TestDecodeInputRequiredIgnoresOrdinaryResults(t *testing.T) {
	for _, raw := range []string{
		`{"content":[]}`,
		`{"content":[{"type":"text","text":"resultType"}]}`,
		`null`,
		``,
	} {
		if _, ok, err := DecodeInputRequired(json.RawMessage(raw)); ok || err != nil {
			t.Fatalf("raw %q: ok=%v err=%v", raw, ok, err)
		}
	}
}

func TestDecodeInputRequiredRejectsEmptyPayload(t *testing.T) {
	_, _, err := DecodeInputRequired(json.RawMessage(`{"resultType":"input_required"}`))
	if err == nil {
		t.Fatal("expected an error when neither inputRequests nor requestState is present")
	}
}

// The capability is derived from the Elicitor so the two cannot drift: a
// declared capability with no handler strands the server.
func TestElicitationCapabilityTracksElicitor(t *testing.T) {
	with, err := NormalizeClientOptions(ClientOptions{
		Mode:     ProtocolModeModern,
		Elicitor: ElicitorFunc(func(context.Context, ElicitRequest) (ElicitResult, error) { return ElicitResult{}, nil }),
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := with.ClientCapabilities["elicitation"]; !ok {
		t.Fatal("expected the elicitation capability to be declared")
	}

	without, err := NormalizeClientOptions(ClientOptions{
		Mode:               ProtocolModeModern,
		ClientCapabilities: map[string]any{"elicitation": map[string]any{}},
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := without.ClientCapabilities["elicitation"]; ok {
		t.Fatal("expected a capability with no handler behind it to be withdrawn")
	}
}
