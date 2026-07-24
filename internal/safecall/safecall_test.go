package safecall

import (
	"errors"
	"testing"
	"time"
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

func TestDoReturnsError(t *testing.T) {
	want := errors.New("boom")
	err := Do("fail", func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestDoRecoversPanic(t *testing.T) {
	err := Do("panic", func() error { panic("unexpected") })
	if err == nil {
		t.Fatal("expected panic recovery error")
	}
}

func TestRecoverConvertsPanic(t *testing.T) {
	var err error
	func() {
		defer Recover("deferred", &err)
		panic("unexpected")
	}()
	if err == nil {
		t.Fatal("expected panic recovery error")
	}
}

func TestGoSafeReportsPanic(t *testing.T) {
	reported := make(chan error, 1)
	cleaned := make(chan struct{})
	GoSafe("goroutine", func(err error) { reported <- err }, func() {
		defer close(cleaned)
		panic("unexpected")
	})
	select {
	case err := <-reported:
		if err == nil {
			t.Fatal("expected panic recovery error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected onPanic to be called")
	}
	<-cleaned // fn's own defers must run before onPanic
}

func TestGoSafeSwallowsOnPanicPanic(t *testing.T) {
	done := make(chan struct{})
	GoSafe("goroutine", func(error) {
		defer close(done)
		panic("onPanic itself panics")
	}, func() {
		panic("unexpected")
	})
	<-done // must not crash the process
}

func TestGoSafeNilOnPanic(t *testing.T) {
	done := make(chan struct{})
	GoSafe("goroutine", nil, func() {
		defer close(done)
		panic("unexpected")
	})
	<-done // recovered silently, process alive
}
