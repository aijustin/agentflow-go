package safecall

import (
	"errors"
	"testing"
)

func TestInvokeReturnsResult(t *testing.T) {
	got, err := Invoke("ok", func() (string, error) {
		return "value", nil
	})
	if err != nil || got != "value" {
		t.Fatalf("unexpected result: %q err=%v", got, err)
	}
}

func TestInvokeReturnsError(t *testing.T) {
	want := errors.New("boom")
	_, err := Invoke("fail", func() (int, error) {
		return 0, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestInvokeRecoversPanic(t *testing.T) {
	_, err := Invoke("panic", func() (struct{}, error) {
		panic("unexpected")
	})
	if err == nil {
		t.Fatal("expected panic recovery error")
	}
}
