package stdout

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type Sink struct {
	mu sync.Mutex
	w  io.Writer
}

func NewSink(w io.Writer) *Sink {
	return &Sink{w: w}
}

func (s *Sink) Emit(ctx context.Context, event core.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.NewEncoder(s.w).Encode(event)
}
