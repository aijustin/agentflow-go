package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// StructuredOutputProtocol is the machine-contract protocol for skill JSON blocks.
const StructuredOutputProtocol = "agentbase.structured_output/v1"

var jsonFenceRE = regexp.MustCompile("(?is)```json\\s*([\\s\\S]*?)```")

// StructuredOutputExtract is the result of scanning assistant text for the
// agentbase.structured_output/v1 machine contract.
type StructuredOutputExtract struct {
	// Block is the last fenced JSON object whose protocol matches StructuredOutputProtocol.
	Block map[string]any
	// OutcomeKind is structured_output | plain_text | error_only | paused.
	OutcomeKind string
	// Error is set when a protocol-claiming fence failed to parse.
	Error string
}

// ExtractStructuredOutput finds the last ```json fence whose parsed object
// has protocol=agentbase.structured_output/v1. It does not guess from prose.
func ExtractStructuredOutput(content string) StructuredOutputExtract {
	text := strings.TrimSpace(content)
	if text == "" {
		return StructuredOutputExtract{OutcomeKind: "plain_text"}
	}

	matches := jsonFenceRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return StructuredOutputExtract{OutcomeKind: "plain_text"}
	}

	var lastBlock map[string]any
	var lastParseErr string
	for _, m := range matches {
		body := strings.TrimSpace(m[1])
		if body == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(body), &obj); err != nil {
			if strings.Contains(body, StructuredOutputProtocol) || strings.Contains(body, `"protocol"`) {
				lastParseErr = fmt.Sprintf("structured_output JSON parse failed: %v", err)
			}
			continue
		}
		proto, _ := obj["protocol"].(string)
		if proto != StructuredOutputProtocol {
			continue
		}
		lastBlock = obj
		lastParseErr = ""
	}

	if lastBlock != nil {
		return StructuredOutputExtract{
			Block:       lastBlock,
			OutcomeKind: "structured_output",
		}
	}
	if lastParseErr != "" {
		return StructuredOutputExtract{
			OutcomeKind: "error_only",
			Error:       lastParseErr,
		}
	}
	return StructuredOutputExtract{OutcomeKind: "plain_text"}
}

// FinalTextFromOutput extracts human-readable assistant text from a step-output
// envelope such as {"text":"..."} or a raw JSON string / plain bytes.
func FinalTextFromOutput(output json.RawMessage) string {
	if len(output) == 0 {
		return ""
	}
	var envelope struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(output, &envelope); err == nil && envelope.Text != "" {
		return envelope.Text
	}
	var asString string
	if err := json.Unmarshal(output, &asString); err == nil {
		return asString
	}
	return string(output)
}
