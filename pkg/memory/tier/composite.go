package tier

import (
	"context"
	"errors"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

// CompositeStore routes tier records across hot, warm, and cold backends.
type CompositeStore struct {
	Hot  Store
	Warm Store
	Cold Store
}

func (c *CompositeStore) Put(ctx context.Context, ns memory.Namespace, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, err := c.storeFor(record.Tier)
	if err != nil {
		return err
	}
	// Write to the destination tier first so a failed Put never loses the
	// record, then remove any stale copies from the other tiers.
	if err := store.Put(ctx, ns, record); err != nil {
		return err
	}
	if record.ID == "" {
		return nil
	}
	return c.deleteFromOtherTiers(ctx, ns, record.ID, record.Tier)
}

// deleteFromOtherTiers removes a record from every tier except keep, ignoring
// not-found and returning the first real error encountered.
func (c *CompositeStore) deleteFromOtherTiers(ctx context.Context, ns memory.Namespace, id string, keep Level) error {
	var lastErr error
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		if level == keep {
			continue
		}
		store, err := c.storeFor(level)
		if err != nil {
			continue
		}
		if err := store.Delete(ctx, ns, id); err != nil && !errors.Is(err, memory.ErrNotFound) {
			lastErr = err
		}
	}
	return lastErr
}

func (c *CompositeStore) Get(ctx context.Context, ns memory.Namespace, id string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		store, err := c.storeFor(level)
		if err != nil {
			continue
		}
		record, err := store.Get(ctx, ns, id)
		if err == nil {
			return record, nil
		}
		if !errors.Is(err, memory.ErrNotFound) {
			return Record{}, err
		}
	}
	return Record{}, memory.ErrNotFound
}

func (c *CompositeStore) List(ctx context.Context, ns memory.Namespace, level Level, limit int) ([]Record, error) {
	store, err := c.storeFor(level)
	if err != nil {
		return nil, nil
	}
	return store.List(ctx, ns, level, limit)
}

func (c *CompositeStore) Delete(ctx context.Context, ns memory.Namespace, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var lastErr error
	found := false
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		store, err := c.storeFor(level)
		if err != nil {
			continue
		}
		if err := store.Delete(ctx, ns, id); err == nil {
			found = true
			continue
		} else if !errors.Is(err, memory.ErrNotFound) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	if !found {
		return memory.ErrNotFound
	}
	return nil
}

func (c *CompositeStore) Count(ctx context.Context, ns memory.Namespace, level Level) (int, error) {
	store, err := c.storeFor(level)
	if err != nil {
		return 0, nil
	}
	return store.Count(ctx, ns, level)
}

func (c *CompositeStore) storeFor(level Level) (Store, error) {
	switch level {
	case LevelHot:
		if c.Hot == nil {
			return nil, memory.ErrNotFound
		}
		return c.Hot, nil
	case LevelWarm:
		if c.Warm == nil {
			return nil, memory.ErrNotFound
		}
		return c.Warm, nil
	case LevelCold:
		if c.Cold == nil {
			return nil, memory.ErrNotFound
		}
		return c.Cold, nil
	default:
		return nil, memory.ErrNotFound
	}
}
