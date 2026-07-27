package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Multi Round-Trip Requests (MRTR), introduced in protocol revision
// 2026-07-28, are how a stateless server asks the client for something in the
// middle of handling a call.
//
// Earlier revisions had the server open its own request back to the client and
// hold the connection open. That cannot survive a load balancer putting the
// retry on a different server instance, so the flow was inverted: instead of
// completing, the server answers with an InputRequiredResult carrying the
// requests it needs answered plus an opaque requestState. The client gathers
// the answers and re-issues the *original* call with both, and any instance
// can pick it up because everything it needs is in the payload.
const (
	// ResultTypeInputRequired marks a result that is a request for input
	// rather than the answer to the call.
	ResultTypeInputRequired = "input_required"

	// MethodElicitationCreate asks the client to collect input from its user.
	MethodElicitationCreate = "elicitation/create"
	// MethodSamplingCreateMessage asks the client to run an LLM completion.
	// Deprecated in 2026-07-28 in favour of the host calling its provider
	// directly, and not supported here.
	MethodSamplingCreateMessage = "sampling/createMessage"
	// MethodRootsList asks the client for its filesystem roots. Deprecated in
	// 2026-07-28 in favour of tool parameters, and not supported here.
	MethodRootsList = "roots/list"
)

// DefaultMaxInputRounds bounds how many times one tool call may be sent back
// for more input. A server that keeps asking would otherwise pin the caller in
// a loop that never produces a result.
const DefaultMaxInputRounds = 8

// InputRequiredResult is returned in place of a call's result when the server
// needs input before it can finish.
type InputRequiredResult struct {
	ResultType    string                     `json:"resultType"`
	InputRequests map[string]json.RawMessage `json:"inputRequests,omitempty"`
	RequestState  json.RawMessage            `json:"requestState,omitempty"`
}

// ElicitAction is the user's answer to an elicitation.
type ElicitAction string

const (
	// ElicitAccept means the user supplied the requested input.
	ElicitAccept ElicitAction = "accept"
	// ElicitDecline means the user explicitly refused.
	ElicitDecline ElicitAction = "decline"
	// ElicitCancel means the user dismissed the request without deciding.
	ElicitCancel ElicitAction = "cancel"
)

// ElicitMode distinguishes the two ways a server may ask for input.
type ElicitMode string

const (
	// ElicitModeForm asks the client to render RequestedSchema as a form. The
	// specification forbids servers from using it for credentials.
	ElicitModeForm ElicitMode = "form"
	// ElicitModeURL sends the user out of band, which is what servers must use
	// for secrets such as passwords or API keys.
	ElicitModeURL ElicitMode = "url"
)

// ElicitRequest is one server request for user input.
type ElicitRequest struct {
	// Key identifies this request within the round trip. It has to be echoed
	// back unchanged so the server can match answers to questions.
	Key string `json:"-"`
	// Mode is form (the default) or url.
	Mode ElicitMode `json:"mode,omitempty"`
	// Message is the prompt shown to the user.
	Message string `json:"message,omitempty"`
	// RequestedSchema describes the shape of the expected content in form mode.
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
	// URL is the out-of-band destination in url mode.
	URL string `json:"url,omitempty"`
}

// ElicitResult is the client's answer. Content is only read when Action is
// ElicitAccept.
type ElicitResult struct {
	Action  ElicitAction    `json:"action"`
	Content json.RawMessage `json:"content,omitempty"`
}

// Elicitor answers a server's request for user input.
//
// It is a host port on purpose: only the host knows whether "ask the user"
// means rendering a form, pausing the run behind a human gate, or refusing
// outright. A client with no Elicitor does not advertise the elicitation
// capability, which the specification requires before a server may ask.
type Elicitor interface {
	Elicit(ctx context.Context, req ElicitRequest) (ElicitResult, error)
}

// ElicitorFunc adapts a function to Elicitor.
type ElicitorFunc func(ctx context.Context, req ElicitRequest) (ElicitResult, error)

func (fn ElicitorFunc) Elicit(ctx context.Context, req ElicitRequest) (ElicitResult, error) {
	return fn(ctx, req)
}

// DecodeInputRequired reports whether raw is an InputRequiredResult and
// decodes it. An ordinary result carries no resultType and is left alone.
func DecodeInputRequired(raw json.RawMessage) (InputRequiredResult, bool, error) {
	if len(raw) == 0 {
		return InputRequiredResult{}, false, nil
	}
	var probe struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		// Not an object, so not an input request; let the caller decode it as
		// the result it expects and report any error from there.
		return InputRequiredResult{}, false, nil
	}
	if probe.ResultType != ResultTypeInputRequired {
		return InputRequiredResult{}, false, nil
	}
	var decoded InputRequiredResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return InputRequiredResult{}, false, fmt.Errorf("mcp: decode input_required result: %w", err)
	}
	if len(decoded.InputRequests) == 0 && len(decoded.RequestState) == 0 {
		return InputRequiredResult{}, false, fmt.Errorf("mcp: input_required result carried neither inputRequests nor requestState")
	}
	return decoded, true, nil
}

// inputRequest is the JSON-RPC request object carried in an inputRequests map.
type inputRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// ResolveInputRequests answers every request in an InputRequiredResult.
//
// It fails closed. A request kind the client cannot answer is an error rather
// than a silently skipped entry, because the server is blocked waiting on it
// and a partial answer set would loop.
func ResolveInputRequests(ctx context.Context, elicitor Elicitor, requests map[string]json.RawMessage) (map[string]any, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	responses := make(map[string]any, len(requests))
	for key, raw := range requests {
		var request inputRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, fmt.Errorf("mcp: decode input request %q: %w", key, err)
		}
		switch request.Method {
		case MethodElicitationCreate:
			if elicitor == nil {
				return nil, fmt.Errorf("mcp: server asked for elicitation %q but no Elicitor is configured", key)
			}
			elicit := ElicitRequest{Key: key}
			if len(request.Params) > 0 {
				if err := json.Unmarshal(request.Params, &elicit); err != nil {
					return nil, fmt.Errorf("mcp: decode elicitation params for %q: %w", key, err)
				}
			}
			elicit.Key = key
			if elicit.Mode == "" {
				elicit.Mode = ElicitModeForm
			}
			result, err := elicitor.Elicit(ctx, elicit)
			if err != nil {
				return nil, fmt.Errorf("mcp: elicitation %q: %w", key, err)
			}
			if result.Action == "" {
				return nil, fmt.Errorf("mcp: elicitation %q returned no action", key)
			}
			responses[key] = result
		case MethodSamplingCreateMessage, MethodRootsList:
			// Both are deprecated in the revision that introduced MRTR, and
			// the client never advertises them, so a server asking is out of
			// contract.
			return nil, fmt.Errorf("mcp: server requested %s for %q, which this client does not support", request.Method, key)
		default:
			return nil, fmt.Errorf("mcp: server requested unsupported input %q for %q", request.Method, key)
		}
	}
	return responses, nil
}

// CallToolInvoker issues one tools/call round trip and returns its raw result.
type CallToolInvoker func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// CallToolWithInput runs a tool call to completion, answering any input the
// server asks for along the way.
//
// Each retry re-sends the original call arguments together with the answers
// and the echoed requestState, which is what lets a different server instance
// resume the work.
func CallToolWithInput(ctx context.Context, req CallToolRequest, options ClientOptions, invoke CallToolInvoker) (CallToolResult, error) {
	params, err := json.Marshal(req)
	if err != nil {
		return CallToolResult{}, err
	}
	maxRounds := options.MaxInputRounds
	if maxRounds <= 0 {
		maxRounds = DefaultMaxInputRounds
	}
	for round := 0; ; round++ {
		raw, err := invoke(ctx, params)
		if err != nil {
			return CallToolResult{}, err
		}
		required, needsInput, err := DecodeInputRequired(raw)
		if err != nil {
			return CallToolResult{}, err
		}
		if !needsInput {
			var result CallToolResult
			if len(raw) == 0 {
				return result, nil
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return CallToolResult{}, fmt.Errorf("mcp: decode tool result: %w", err)
			}
			return result, nil
		}
		if options.Mode != ProtocolModeModern {
			return CallToolResult{}, fmt.Errorf("mcp: server returned an input_required result, which requires protocol %s; this client is in %s mode", ProtocolVersionModern, options.Mode)
		}
		if round+1 >= maxRounds {
			return CallToolResult{}, fmt.Errorf("mcp: tool call exceeded %d input rounds", maxRounds)
		}
		responses, err := ResolveInputRequests(ctx, options.Elicitor, required.InputRequests)
		if err != nil {
			return CallToolResult{}, err
		}
		if params, err = retryParams(req, responses, required.RequestState); err != nil {
			return CallToolResult{}, err
		}
	}
}

// retryParams rebuilds the original call arguments with the gathered answers
// and the server's opaque state attached.
func retryParams(req CallToolRequest, responses map[string]any, state json.RawMessage) (json.RawMessage, error) {
	body := map[string]any{"name": req.Name}
	if len(req.Arguments) > 0 {
		body["arguments"] = req.Arguments
	}
	if len(responses) > 0 {
		body["inputResponses"] = responses
	}
	if len(state) > 0 {
		body["requestState"] = state
	}
	merged, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode input responses: %w", err)
	}
	return merged, nil
}
