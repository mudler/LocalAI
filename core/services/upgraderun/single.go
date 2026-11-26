package upgraderun

import (
	"context"
	"fmt"
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

// SingleState is what the caller of StartOne answers for its item.
type SingleState string

const (
	// SingleStarted: the operation of the item was accepted; JobID is set.
	SingleStarted SingleState = "started"
	// SingleRefused: the item ended without an operation (its start was
	// refused or skipped).
	SingleRefused SingleState = "refused"
	// SinglePending: the wait ended before the operation was accepted. The
	// run goes on and starts the item later; the caller answers with the run.
	SinglePending SingleState = "pending"
)

// SingleResult is the outcome of a run of one item, as the routes and the
// Assistant answer it.
type SingleResult struct {
	State SingleState
	Run   Run
	Item  Item
	RunID string
	JobID string
	// ErrorKey and Error describe a refusal.
	ErrorKey string
	Error    string
}

// SingleOutcome maps the run and the item StartOne returned to a job, a
// refusal or a pending start. Every caller of StartOne uses it, so none of
// them answers "started" without a job.
func SingleOutcome(run Run, it Item) SingleResult {
	out := SingleResult{Run: run, Item: it, RunID: run.ID}
	switch {
	case it.JobID != "":
		out.State, out.JobID = SingleStarted, it.JobID
	case it.Phase.Terminal():
		out.State, out.ErrorKey, out.Error = SingleRefused, orUnknown(it.ErrorKey), it.Error
	default:
		out.State = SinglePending
	}
	return out
}

// Err is nil for a started item, a *RefusedError for a refused one and a
// *PendingError for a pending one.
func (r SingleResult) Err() error {
	switch r.State {
	case SingleStarted:
		return nil
	case SingleRefused:
		return &RefusedError{Key: r.Item.Key, ErrorKey: r.ErrorKey, Message: r.Error}
	}
	return &PendingError{RunID: r.RunID, Key: r.Item.Key}
}

// RefusedError is an item of a run of one that ended without an operation.
type RefusedError struct {
	Key      string
	ErrorKey string
	Message  string
}

func (e *RefusedError) Error() string { return e.ErrorKey + ": " + e.Message }

// PendingError is an item of a run of one whose operation was not accepted
// within the wait. The run keeps it and starts it later.
type PendingError struct {
	RunID string
	Key   string
}

func (e *PendingError) Error() string {
	return fmt.Sprintf("the update of %s waits in upgrade run %s and has no operation yet; follow the run", e.Key, e.RunID)
}
