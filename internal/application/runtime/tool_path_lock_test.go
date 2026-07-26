package runtime

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestLockPathForArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "file_path", input: `{"file_path":"/a/b.go"}`, want: "/a/b.go"},
		{name: "path", input: `{"path":"pkg/x.go"}`, want: "pkg/x.go"},
		{name: "target_file", input: `{"target_file":"main.go"}`, want: "main.go"},
		{name: "priority file_path", input: `{"path":"b","file_path":"a"}`, want: "a"},
		{name: "directory key ignored", input: `{"target_directory":"pkg"}`, want: ""},
		{name: "empty", input: `{}`, want: ""},
		{name: "invalid", input: `not-json`, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := lockPathForArgs(json.RawMessage(tc.input))
			if got != tc.want {
				t.Fatalf("lockPathForArgs(%s)=%q want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPathLockSetSerializesSamePath(t *testing.T) {
	t.Parallel()
	locks := newKeyedLockSet()
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			unlock := locks.acquire("/same.go")
			defer unlock()
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}(i)
	}
	close(start)
	wg.Wait()
	if len(order) != 2 {
		t.Fatalf("expected 2 completions, got %v", order)
	}
}
