package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
)

type Sink struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

func NewSink(path string) (*Sink, error) {
	if path == "" {
		return nil, fmt.Errorf("audit file: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Sink{path: path, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (sink *Sink) Record(ctx context.Context, event audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	event = audit.CloneEvent(event).WithDefaults(sink.now())
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("audit file: marshal event: %w", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	file, err := os.OpenFile(sink.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}
