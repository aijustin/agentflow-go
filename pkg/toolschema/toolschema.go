// Package toolschema provides a lightweight, dependency-free validator for
// tool call inputs against a tool's declared JSON-Schema-like InputSchema.
//
// It supports the common subset used by tool manifests: object types with
// required properties and additionalProperties control, arrays with item
// schemas and cardinality/uniqueness constraints, the primitive types
// (string, number, integer, boolean, null), union "type" arrays (e.g.
// ["string","null"]), enum membership, string length/pattern constraints, and
// numeric range/multipleOf constraints.
//
// Anything the validator does not understand (unknown keywords, unknown type
// tokens, a malformed schema, or a malformed regex pattern) is treated as "no
// constraint" so a schema quirk never blocks a tool call that the executor
// could otherwise handle.
package toolschema

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
)

type schemaNode struct {
	Type                 json.RawMessage       `json:"type"`
	Enum                 []json.RawMessage     `json:"enum"`
	Required             []string              `json:"required"`
	Properties           map[string]schemaNode `json:"properties"`
	AdditionalProperties *additionalProps      `json:"additionalProperties"`
	Items                *schemaNode           `json:"items"`
	MinItems             *int                  `json:"minItems"`
	MaxItems             *int                  `json:"maxItems"`
	UniqueItems          bool                  `json:"uniqueItems"`
	MinLength            *int                  `json:"minLength"`
	MaxLength            *int                  `json:"maxLength"`
	Pattern              string                `json:"pattern"`
	Minimum              *float64              `json:"minimum"`
	Maximum              *float64              `json:"maximum"`
	ExclusiveMinimum     *float64              `json:"exclusiveMinimum"`
	ExclusiveMaximum     *float64              `json:"exclusiveMaximum"`
	MultipleOf           *float64              `json:"multipleOf"`
}

// additionalProps captures the two legal shapes of the additionalProperties
// keyword: a boolean (true/false) or a nested schema. A value that is neither
// leaves set=false, which the validator treats as "no constraint".
type additionalProps struct {
	allowed bool
	schema  *schemaNode
	set     bool
}

func (a *additionalProps) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		a.allowed = b
		a.set = true
		return nil
	}
	var s schemaNode
	if err := json.Unmarshal(data, &s); err == nil {
		a.schema = &s
		a.allowed = true
		a.set = true
	}
	return nil
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
	types := parseTypes(node.Type)

	// Preserve the original behavior where an absent value against an object
	// schema is validated as an empty object (so required fields still fire),
	// unless the schema explicitly permits null.
	if value == nil && !containsType(types, "null") &&
		(containsType(types, "object") || len(node.Required) > 0 || len(node.Properties) > 0) {
		value = map[string]any{}
	}

	if len(node.Enum) > 0 && !enumContains(node.Enum, value) {
		return fmt.Errorf("%s: value is not one of the allowed enum values", path)
	}

	if len(types) > 0 && !matchesAnyType(types, value) {
		return typeError(path, strings.Join(types, "|"), value)
	}

	switch v := value.(type) {
	case string:
		return validateString(node, v, path)
	case float64:
		return validateNumber(node, v, path)
	case []any:
		return validateArray(node, v, path)
	case map[string]any:
		return validateObject(node, v, path)
	default:
		return nil
	}
}

func validateString(node schemaNode, s, path string) error {
	if node.MinLength != nil && len([]rune(s)) < *node.MinLength {
		return fmt.Errorf("%s: string is shorter than minLength %d", path, *node.MinLength)
	}
	if node.MaxLength != nil && len([]rune(s)) > *node.MaxLength {
		return fmt.Errorf("%s: string is longer than maxLength %d", path, *node.MaxLength)
	}
	if node.Pattern != "" {
		re, err := regexp.Compile(node.Pattern)
		if err != nil {
			return nil
		}
		if !re.MatchString(s) {
			return fmt.Errorf("%s: string does not match pattern %q", path, node.Pattern)
		}
	}
	return nil
}

func validateNumber(node schemaNode, f float64, path string) error {
	if node.Minimum != nil && f < *node.Minimum {
		return fmt.Errorf("%s: %v is below minimum %v", path, f, *node.Minimum)
	}
	if node.Maximum != nil && f > *node.Maximum {
		return fmt.Errorf("%s: %v is above maximum %v", path, f, *node.Maximum)
	}
	if node.ExclusiveMinimum != nil && f <= *node.ExclusiveMinimum {
		return fmt.Errorf("%s: %v is not greater than exclusiveMinimum %v", path, f, *node.ExclusiveMinimum)
	}
	if node.ExclusiveMaximum != nil && f >= *node.ExclusiveMaximum {
		return fmt.Errorf("%s: %v is not less than exclusiveMaximum %v", path, f, *node.ExclusiveMaximum)
	}
	if node.MultipleOf != nil && *node.MultipleOf != 0 {
		q := f / *node.MultipleOf
		if math.Abs(q-math.Round(q)) > 1e-9 {
			return fmt.Errorf("%s: %v is not a multiple of %v", path, f, *node.MultipleOf)
		}
	}
	return nil
}

func validateArray(node schemaNode, arr []any, path string) error {
	if node.MinItems != nil && len(arr) < *node.MinItems {
		return fmt.Errorf("%s: has fewer than minItems %d", path, *node.MinItems)
	}
	if node.MaxItems != nil && len(arr) > *node.MaxItems {
		return fmt.Errorf("%s: has more than maxItems %d", path, *node.MaxItems)
	}
	if node.UniqueItems && !itemsUnique(arr) {
		return fmt.Errorf("%s: items are not unique", path)
	}
	if node.Items != nil {
		for i, item := range arr {
			if err := validateValue(*node.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateObject(node schemaNode, obj map[string]any, path string) error {
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
	if node.AdditionalProperties != nil && node.AdditionalProperties.set {
		for name, v := range obj {
			if _, declared := node.Properties[name]; declared {
				continue
			}
			if !node.AdditionalProperties.allowed {
				return fmt.Errorf("%s: unexpected property %q", path, name)
			}
			if node.AdditionalProperties.schema != nil {
				if err := validateValue(*node.AdditionalProperties.schema, v, path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// parseTypes normalizes the "type" keyword, which may be a single string or an
// array of strings, into a slice. An empty or unparseable value yields nil,
// meaning "no type constraint".
func parseTypes(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil
		}
		return []string{single}
	}
	var multi []string
	if err := json.Unmarshal(raw, &multi); err == nil {
		return multi
	}
	return nil
}

func containsType(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

func matchesAnyType(types []string, value any) bool {
	for _, t := range types {
		if matchesType(t, value) {
			return true
		}
	}
	return false
}

func matchesType(t string, value any) bool {
	switch t {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == math.Trunc(f)
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "null":
		return value == nil
	default:
		// Unknown/"any" type token imposes no constraint.
		return true
	}
}

func enumContains(enum []json.RawMessage, value any) bool {
	for _, raw := range enum {
		var candidate any
		if err := json.Unmarshal(raw, &candidate); err != nil {
			continue
		}
		if reflect.DeepEqual(candidate, value) {
			return true
		}
	}
	return false
}

func itemsUnique(arr []any) bool {
	seen := make(map[string]struct{}, len(arr))
	for _, item := range arr {
		key, err := json.Marshal(item)
		if err != nil {
			continue
		}
		if _, dup := seen[string(key)]; dup {
			return false
		}
		seen[string(key)] = struct{}{}
	}
	return true
}

func typeError(path, want string, value any) error {
	return fmt.Errorf("%s: expected %s, got %T", path, want, value)
}
