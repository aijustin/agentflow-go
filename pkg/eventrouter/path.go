package eventrouter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func valueAtPath(raw json.RawMessage, path string) (any, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if len(raw) == 0 {
			return nil, false, nil
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, false, err
		}
		return value, true, nil
	}
	if len(raw) == 0 {
		return nil, false, nil
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false, fmt.Errorf("decode payload: %w", err)
	}
	current := root
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, nil
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false, nil
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false, nil
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func stringAtPath(raw json.RawMessage, path string) (string, bool, error) {
	value, ok, err := valueAtPath(raw, path)
	if err != nil || !ok {
		return "", ok, err
	}
	switch typed := value.(type) {
	case string:
		return typed, true, nil
	case json.Number:
		return typed.String(), true, nil
	case bool:
		if typed {
			return "true", true, nil
		}
		return "false", true, nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return "", false, err
		}
		return string(raw), true, nil
	}
}

func rawAtPath(raw json.RawMessage, path string) (json.RawMessage, bool, error) {
	value, ok, err := valueAtPath(raw, path)
	if err != nil || !ok {
		return nil, ok, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}
