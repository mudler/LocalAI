package upgraderun

import (
	"context"
	"time"
)

// DefaultStartWait bounds how long StartOne waits for the operation of its
// item to be accepted.
const DefaultStartWait = 30 * time.Second

// startPoll is how often StartOne reads the run while it waits.
const startPoll = 25 * time.Millisecond

// StartOne starts a run of one item and waits until the operation of the item
// is accepted (the item has a job id), the item ended without one (its start
// was refused or skipped), or wait runs out. The routes that update one item
// at a time use it: their callers expect the id of the operation in the
// answer, and the runner starts the operation on its own goroutine.
//
// The returned item is the state of the item when the wait ended. A zero
// wait uses DefaultStartWait.
func (s *Service) StartOne(ctx context.Context, actor string, si StartItem, source Source, wait time.Duration) (Run, Item, error) {
	run, err := s.Start(ctx, actor, StartRequest{Items: []StartItem{si}, Source: source})
	if err != nil {
		return Run{}, Item{}, err
	}
	key := Key(si.Kind, si.Name)
	if wait <= 0 {
		wait = DefaultStartWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(startPoll)
	defer tick.Stop()
	for {
		if it := run.Item(key); it != nil && (it.JobID != "" || it.Phase.Terminal() || !run.Active()) {
			return run, *it, nil
		}
		select {
		case <-ctx.Done():
			return s.itemOf(ctx, run, key)
		case <-deadline.C:
			return s.itemOf(ctx, run, key)
		case <-tick.C:
		}
		if cur, err := s.Get(ctx, run.ID); err == nil {
			run = cur
		}
	}
}

func (s *Service) itemOf(ctx context.Context, run Run, key string) (Run, Item, error) {
	if cur, err := s.Get(context.WithoutCancel(ctx), run.ID); err == nil {
		run = cur
	}
	if it := run.Item(key); it != nil {
		return run, *it, nil
	}
	return run, Item{Key: key}, nil
}
