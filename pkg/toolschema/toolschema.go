// Package toolschema provides a lightweight, dependency-free validator for
// tool call inputs against a tool's declared JSON-Schema-like InputSchema.
//
// It intentionally supports only the common subset used by tool manifests:
// object types with required properties, arrays with item schemas, and the
// primitive types (string, number, integer, boolean, null). Anything the
// validator does not understand (unknown keywords, missing "type", malformed
// schema) is treated as "no constraint" so a schema quirk never blocks a tool
// call that the executor could otherwise handle.
package toolschema

import (
	"encoding/json"
	"fmt"
	"math"
)

type schemaNode struct {
	Type       string                `json:"type"`
	Required   []string              `json:"required"`
	Properties map[string]schemaNode `json:"properties"`
	Items      *schemaNode           `json:"items"`
}

// Validate reports whether input satisfies schema. An empty schema only
// requires that input (when present) is valid JSON. A malformed schema is a
// scenario configuration concern rather than a tool-input error, so it is
// treated as "no constraint".
func Validate(schema, input json.RawMessage) error {
	if len(schema) == 0 {
		return ensureValidJSON(input)
	}
	var node schemaNode
	if err := json.Unmarshal(schema, &node); err != nil {
		return nil
	}
	var value any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &value); err != nil {
			return fmt.Errorf("tool input is not valid JSON: %w", err)
		}
	}
	return validateValue(node, value, "input")
}

func ensureValidJSON(input json.RawMessage) error {
	if len(input) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return fmt.Errorf("tool input is not valid JSON: %w", err)
	}
	return nil
}

func validateValue(node schemaNode, value any, path string) error {
	switch node.Type {
	case "", "any":
		return nil
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			if value == nil {
				obj = map[string]any{}
			} else {
				return typeError(path, "object", value)
			}
		}
		for _, req := range node.Required {
			if _, exists := obj[req]; !exists {
				return fmt.Errorf("%s: missing required field %q", path, req)
			}
		}
		for name, prop := range node.Properties {
			if v, exists := obj[name]; exists {
				if err := validateValue(prop, v, path+"."+name); err != nil {
					return err
				}
			}
		}
		return nil
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return typeError(path, "array", value)
		}
		if node.Items != nil {
			for i, item := range arr {
				if err := validateValue(*node.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		return nil
	case "string":
		if _, ok := value.(string); !ok {
			return typeError(path, "string", value)
		}
		return nil
	case "integer":
		f, ok := value.(float64)
		if !ok || f != math.Trunc(f) {
			return typeError(path, "integer", value)
		}
		return nil
	case "number":
		if _, ok := value.(float64); !ok {
			return typeError(path, "number", value)
		}
		return nil
	case "boolean":
		if _, ok := value.(bool); !ok {
			return typeError(path, "boolean", value)
		}
		return nil
	case "null":
		if value != nil {
			return typeError(path, "null", value)
		}
		return nil
	default:
		return nil
	}
}

func typeError(path, want string, value any) error {
	return fmt.Errorf("%s: expected %s, got %T", path, want, value)
}
