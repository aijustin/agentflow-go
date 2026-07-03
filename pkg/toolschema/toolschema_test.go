package toolschema

import (
	"encoding/json"
	"testing"
)

func TestValidateEmptySchemaAcceptsAnyValidJSON(t *testing.T) {
	if err := Validate(nil, json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("empty schema should accept valid JSON, got %v", err)
	}
	if err := Validate(nil, nil); err != nil {
		t.Fatalf("empty schema and input should be accepted, got %v", err)
	}
	if err := Validate(nil, json.RawMessage(`{bad`)); err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
}

func TestValidateRequiredFields(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`)
	if err := Validate(schema, json.RawMessage(`{"name":"x"}`)); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected missing required field to be rejected")
	}
	if err := Validate(schema, nil); err == nil {
		t.Fatal("expected empty input to fail required check")
	}
}

func TestValidateTypeChecks(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"flag":{"type":"boolean"},"items":{"type":"array","items":{"type":"string"}}}}`)
	if err := Validate(schema, json.RawMessage(`{"count":3,"flag":true,"items":["a","b"]}`)); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"count":3.5}`)); err == nil {
		t.Fatal("expected non-integer to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`{"flag":"yes"}`)); err == nil {
		t.Fatal("expected non-boolean to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`{"items":[1,2]}`)); err == nil {
		t.Fatal("expected wrong array item type to be rejected")
	}
}

func TestValidateMalformedSchemaIsNoConstraint(t *testing.T) {
	if err := Validate(json.RawMessage(`not-json`), json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("malformed schema should not block valid JSON, got %v", err)
	}
}

func TestValidateRejectsInvalidJSONInput(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	if err := Validate(schema, json.RawMessage(`{bad`)); err == nil {
		t.Fatal("expected invalid JSON input to be rejected")
	}
}

func TestValidateEnum(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["read","write"]}}}`)
	if err := Validate(schema, json.RawMessage(`{"mode":"read"}`)); err != nil {
		t.Fatalf("valid enum value rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"mode":"delete"}`)); err == nil {
		t.Fatal("expected value outside enum to be rejected")
	}
}

func TestValidateUnionTypeAllowsNull(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"note":{"type":["string","null"]}}}`)
	if err := Validate(schema, json.RawMessage(`{"note":"hi"}`)); err != nil {
		t.Fatalf("string in union rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"note":null}`)); err != nil {
		t.Fatalf("null in union rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"note":5}`)); err == nil {
		t.Fatal("expected number to be rejected by string|null union")
	}
}

func TestValidateStringLengthAndPattern(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","minLength":2,"maxLength":4,"pattern":"^[a-z]+$"}}}`)
	if err := Validate(schema, json.RawMessage(`{"code":"abc"}`)); err != nil {
		t.Fatalf("valid string rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"code":"a"}`)); err == nil {
		t.Fatal("expected minLength violation to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`{"code":"abcde"}`)); err == nil {
		t.Fatal("expected maxLength violation to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`{"code":"AB"}`)); err == nil {
		t.Fatal("expected pattern violation to be rejected")
	}
}

func TestValidateMalformedPatternIsNoConstraint(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","pattern":"([unclosed"}}}`)
	if err := Validate(schema, json.RawMessage(`{"code":"anything"}`)); err != nil {
		t.Fatalf("malformed pattern should impose no constraint, got %v", err)
	}
}

func TestValidateNumberBounds(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","minimum":1,"maximum":10,"multipleOf":0.5}}}`)
	if err := Validate(schema, json.RawMessage(`{"n":2.5}`)); err != nil {
		t.Fatalf("valid number rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"n":0}`)); err == nil {
		t.Fatal("expected below-minimum to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`{"n":11}`)); err == nil {
		t.Fatal("expected above-maximum to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`{"n":2.3}`)); err == nil {
		t.Fatal("expected non-multiple to be rejected")
	}
}

func TestValidateExclusiveBounds(t *testing.T) {
	schema := json.RawMessage(`{"type":"integer","exclusiveMinimum":0,"exclusiveMaximum":5}`)
	if err := Validate(schema, json.RawMessage(`3`)); err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`0`)); err == nil {
		t.Fatal("expected exclusiveMinimum boundary to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`5`)); err == nil {
		t.Fatal("expected exclusiveMaximum boundary to be rejected")
	}
}

func TestValidateArrayConstraints(t *testing.T) {
	schema := json.RawMessage(`{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":3,"uniqueItems":true}`)
	if err := Validate(schema, json.RawMessage(`["a","b"]`)); err != nil {
		t.Fatalf("valid array rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`[]`)); err == nil {
		t.Fatal("expected minItems violation to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`["a","b","c","d"]`)); err == nil {
		t.Fatal("expected maxItems violation to be rejected")
	}
	if err := Validate(schema, json.RawMessage(`["a","a"]`)); err == nil {
		t.Fatal("expected duplicate items to be rejected")
	}
}

func TestValidateAdditionalPropertiesFalse(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"additionalProperties":false}`)
	if err := Validate(schema, json.RawMessage(`{"name":"x"}`)); err != nil {
		t.Fatalf("declared property rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"name":"x","extra":1}`)); err == nil {
		t.Fatal("expected undeclared property to be rejected")
	}
}

func TestValidateAdditionalPropertiesSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"additionalProperties":{"type":"integer"}}`)
	if err := Validate(schema, json.RawMessage(`{"name":"x","age":30}`)); err != nil {
		t.Fatalf("valid additional property rejected: %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{"name":"x","age":"old"}`)); err == nil {
		t.Fatal("expected additional property violating its schema to be rejected")
	}
}

func TestValidateOptionalObjectWithEmptyInput(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	if err := Validate(schema, nil); err != nil {
		t.Fatalf("empty input against all-optional object should pass, got %v", err)
	}
	if err := Validate(schema, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("empty object should pass, got %v", err)
	}
}
