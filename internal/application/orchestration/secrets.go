package orchestration

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var secretRefPattern = regexp.MustCompile(`\$\{secret:([A-Za-z0-9_.-]+)\}`)

func resolveRuntimeSecrets(raw json.RawMessage, secrets map[string]string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return substituteSecretString(string(raw), secrets)
	}
	resolved, err := resolveSecretValue(value, secrets)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("orchestration: marshal secret-resolved input: %w", err)
	}
	return out, nil
}

func resolveSecretValue(value any, secrets map[string]string) (any, error) {
	switch typed := value.(type) {
	case string:
		return substituteSecretInString(typed, secrets)
	case map[string]any:
		for key, child := range typed {
			resolved, err := resolveSecretValue(child, secrets)
			if err != nil {
				return nil, err
			}
			typed[key] = resolved
		}
		return typed, nil
	case []any:
		for index, child := range typed {
			resolved, err := resolveSecretValue(child, secrets)
			if err != nil {
				return nil, err
			}
			typed[index] = resolved
		}
		return typed, nil
	default:
		return value, nil
	}
}

func substituteSecretString(value string, secrets map[string]string) (json.RawMessage, error) {
	resolved, err := substituteSecretInString(value, secrets)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(resolved), nil
}

func substituteSecretInString(value string, secrets map[string]string) (string, error) {
	if !strings.Contains(value, "${secret:") {
		return value, nil
	}
	missing := false
	out := secretRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		sub := secretRefPattern.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		secret, ok := secrets[sub[1]]
		if !ok {
			missing = true
			return match
		}
		return secret
	})
	if missing {
		return "", fmt.Errorf("orchestration: unresolved runtime secret reference in %q", value)
	}
	return out, nil
}
