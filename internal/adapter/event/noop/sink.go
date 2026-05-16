package noop

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type Sink struct{}

func NewSink() Sink {
	return Sink{}
}

func (Sink) Emit(context.Context, core.Event) error {
	return nil
}
