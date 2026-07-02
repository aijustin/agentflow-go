package eventrouter

import (
	"encoding/json"
	"testing"
)

func TestValueAtPathNestedAndArray(t *testing.T) {
	raw := json.RawMessage(`{"body":{"items":[{"name":"first"}]},"count":3,"ok":true}`)
	got, ok, err := stringAtPath(raw, "body.items.0.name")
	if err != nil || !ok || got != "first" {
		t.Fatalf("unexpected array path result: %q ok=%v err=%v", got, ok, err)
	}
	got, ok, err = stringAtPath(raw, "count")
	if err != nil || !ok || got != "3" {
		t.Fatalf("unexpected number path result: %q ok=%v err=%v", got, ok, err)
	}
	got, ok, err = stringAtPath(raw, "ok")
	if err != nil || !ok || got != "true" {
		t.Fatalf("unexpected bool path result: %q ok=%v err=%v", got, ok, err)
	}
}

func TestValueAtPathMissingAndInvalid(t *testing.T) {
	raw := json.RawMessage(`{"a":1}`)
	_, ok, err := stringAtPath(raw, "missing.path")
	if err != nil || ok {
		t.Fatalf("expected missing path, ok=%v err=%v", ok, err)
	}
	_, _, err = stringAtPath(json.RawMessage(`{`), "a")
	if err == nil {
		t.Fatal("expected decode error")
	}
	_, ok, err = stringAtPath(nil, "")
	if err != nil || ok {
		t.Fatalf("expected empty payload, ok=%v err=%v", ok, err)
	}
}

func TestRawAtPathReturnsEncodedValue(t *testing.T) {
	raw := json.RawMessage(`{"body":{"ticket_id":"T-1"}}`)
	value, ok, err := rawAtPath(raw, "body")
	if err != nil || !ok {
		t.Fatalf("unexpected error: ok=%v err=%v", ok, err)
	}
	var body map[string]string
	if err := json.Unmarshal(value, &body); err != nil || body["ticket_id"] != "T-1" {
		t.Fatalf("unexpected raw value: %s err=%v", value, err)
	}
}
