package core

import "encoding/json"

// BuildLifecyclePayload returns a lifecycle event payload that stably includes
// Episode/Session correlation. Terminal events use RunTerminalPayload; other
// lifecycle events merge correlation into an object payload.
func BuildLifecyclePayload(typ EventType, payload json.RawMessage, corr EpisodeCorrelation) json.RawMessage {
	if !IsLifecycleEvent(typ) {
		return payload
	}
	switch typ {
	case EventRunCompleted:
		return mustJSON(buildRunCompletedPayload(payload, corr, nil))
	case EventRunFailed:
		return mustJSON(RunTerminalPayload{
			Status:            "failed",
			OutcomeKind:       "error_only",
			Error:             extractLifecycleError(payload),
			TerminationReason: extractLifecycleTerminationReason(payload, TerminationReasonError),
			EpisodeID:         corr.EpisodeID,
			TriggerKind:       corr.TriggerKind,
			SessionID:         corr.SessionID,
		})
	case EventRunCancelled:
		return mustJSON(RunTerminalPayload{
			Status:            "cancelled",
			OutcomeKind:       "error_only",
			TerminationReason: extractLifecycleTerminationReason(payload, TerminationReasonCancelled),
			EpisodeID:         corr.EpisodeID,
			TriggerKind:       corr.TriggerKind,
			SessionID:         corr.SessionID,
		})
	default:
		return mergeLifecycleCorrelation(payload, corr)
	}
}

// BuildRunCompletedPayload builds an AF-REQ-03 terminal payload with optional usage.
func BuildRunCompletedPayload(output json.RawMessage, corr EpisodeCorrelation, usage *RunUsage) json.RawMessage {
	return mustJSON(buildRunCompletedPayload(output, corr, usage))
}

func buildRunCompletedPayload(output json.RawMessage, corr EpisodeCorrelation, usage *RunUsage) RunTerminalPayload {
	finalText := FinalTextFromOutput(output)
	ext := ExtractStructuredOutput(finalText)
	payload := RunTerminalPayload{
		Status:            "completed",
		OutcomeKind:       ext.OutcomeKind,
		FinalText:         finalText,
		Output:            cloneRaw(output),
		TerminationReason: TerminationReasonCompleted,
		Usage:             usage,
		EpisodeID:         corr.EpisodeID,
		TriggerKind:       corr.TriggerKind,
		SessionID:         corr.SessionID,
	}
	if ext.Block != nil {
		payload.StructuredOutput = mustJSON(ext.Block)
	}
	if ext.Error != "" {
		payload.StructuredOutputError = ext.Error
		if payload.OutcomeKind == "error_only" {
			payload.Status = "failed"
			payload.TerminationReason = TerminationReasonError
		}
	}
	return payload
}

// BuildPausedOutcomePayload returns a terminal-shaped payload used when a run
// ends paused (SSE end parity). EventStore still emits EventRunPaused separately.
func BuildPausedOutcomePayload(corr EpisodeCorrelation) json.RawMessage {
	return mustJSON(RunTerminalPayload{
		Status:      "paused",
		OutcomeKind: "paused",
		EpisodeID:   corr.EpisodeID,
		TriggerKind: corr.TriggerKind,
		SessionID:   corr.SessionID,
	})
}

func mergeLifecycleCorrelation(payload json.RawMessage, corr EpisodeCorrelation) json.RawMessage {
	fields := map[string]json.RawMessage{}
	if len(payload) > 0 && json.Valid(payload) && payload[0] == '{' {
		_ = json.Unmarshal(payload, &fields)
	} else if len(payload) > 0 {
		fields["data"] = cloneRaw(payload)
	}
	if corr.EpisodeID != "" {
		fields["episode_id"] = mustJSON(corr.EpisodeID)
	}
	if corr.TriggerKind != "" {
		fields["trigger_kind"] = mustJSON(corr.TriggerKind)
	}
	if corr.SessionID != "" {
		fields["session_id"] = mustJSON(corr.SessionID)
	}
	// AF-REQ-06: lifecycle start/resume payloads must not be nil.
	if len(fields) == 0 {
		return json.RawMessage(`{}`)
	}
	return mustJSON(fields)
}

func extractLifecycleError(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Error != "" {
		return envelope.Error
	}
	var asString string
	if err := json.Unmarshal(payload, &asString); err == nil {
		return asString
	}
	return string(payload)
}

// extractLifecycleTerminationReason pulls the emitter-supplied
// termination_reason out of a terminal payload, falling back to the
// event-type default when older emitters did not classify the cause.
func extractLifecycleTerminationReason(payload json.RawMessage, fallback string) string {
	if len(payload) > 0 {
		var envelope struct {
			TerminationReason string `json:"termination_reason"`
		}
		if err := json.Unmarshal(payload, &envelope); err == nil && envelope.TerminationReason != "" {
			return envelope.TerminationReason
		}
	}
	return fallback
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make(json.RawMessage, len(value))
	copy(clone, value)
	return clone
}
