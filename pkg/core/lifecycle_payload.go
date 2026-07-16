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
		return mustJSON(RunTerminalPayload{
			Status:      "completed",
			Output:      cloneRaw(payload),
			EpisodeID:   corr.EpisodeID,
			TriggerKind: corr.TriggerKind,
			SessionID:   corr.SessionID,
		})
	case EventRunFailed:
		return mustJSON(RunTerminalPayload{
			Status:      "failed",
			Error:       extractLifecycleError(payload),
			EpisodeID:   corr.EpisodeID,
			TriggerKind: corr.TriggerKind,
			SessionID:   corr.SessionID,
		})
	case EventRunCancelled:
		return mustJSON(RunTerminalPayload{
			Status:      "cancelled",
			EpisodeID:   corr.EpisodeID,
			TriggerKind: corr.TriggerKind,
			SessionID:   corr.SessionID,
		})
	default:
		return mergeLifecycleCorrelation(payload, corr)
	}
}

func mergeLifecycleCorrelation(payload json.RawMessage, corr EpisodeCorrelation) json.RawMessage {
	if corr.Empty() && len(payload) == 0 {
		return nil
	}
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
	if len(fields) == 0 {
		return nil
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
