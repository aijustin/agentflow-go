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
