package tier

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

// MigrationObserver receives tier migration notifications from a Manager.
type MigrationObserver interface {
	Promoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level)
	Demoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level)
	Evicted(ctx context.Context, ns memory.Namespace, recordID string, from Level)
}

// NoopMigrationObserver discards migration notifications.
type NoopMigrationObserver struct{}

func (NoopMigrationObserver) Promoted(context.Context, memory.Namespace, string, Level, Level) {}
func (NoopMigrationObserver) Demoted(context.Context, memory.Namespace, string, Level, Level)  {}
func (NoopMigrationObserver) Evicted(context.Context, memory.Namespace, string, Level)         {}
