// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
)

// Executor is owned by the host. It revalidates authorization before inference,
// stores artifacts before returning, and reports only bounded progress text.
type Executor interface {
	Execute(context.Context, string, string, Arguments, func(string)) (Result, error)
}

type Result struct {
	Outputs     []Value  `json:"outputs"`
	Explanation string   `json:"explanation,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

type Command struct {
	Action     CommandAction `json:"action"`
	Name       string        `json:"name,omitempty"`
	Branch     string        `json:"branch,omitempty"`
	Checkpoint string        `json:"checkpoint,omitempty"`
	Node       string        `json:"node,omitempty"`
	Graph      *Graph        `json:"graph,omitempty"`
	Index      int           `json:"index,omitempty"`
	Text       *string       `json:"text,omitempty"`
	Continue   bool          `json:"continue,omitempty"`
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Runner belongs to the singleton add-on owner. Its live goroutines are local;
// their durable attempts are not automatically repeated after an owner restart.
type Runner struct {
	store    *Store
	executor Executor
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	runs     map[string]*activeRun
}

func NewRunner(ctx context.Context, store *Store, executor Executor) (*Runner, error) {
	ctx, cancel := context.WithCancel(ctx)
	r := &Runner{
		store:    store,
		executor: executor,
		ctx:      ctx,
		cancel:   cancel,
		runs:     map[string]*activeRun{},
	}

	ids, err := store.IDs(ctx)
	if err != nil {
		cancel()
		return nil, err
	}

	for _, id := range ids {
		current, readErr := store.Read(ctx, id)
		if readErr != nil {
			cancel()
			return nil, readErr
		}

		busy := false
		for _, b := range current.Branches {
			busy = busy || b.Current().Busy()
		}
		if !busy {
			continue
		}

		_, err := r.update(ctx, id, func(w *Workspace) error {
			for i := range w.Branches {
				b := w.Branches[i]
				s := b.Current()
				if !s.Busy() {
					continue
				}

				var events []Event
				for name, n := range s.Nodes {
					if n.Status() != Running {
						continue
					}

					events = append(events, Event{
						Type:      schema.ExecutionEvent,
						Action:    schema.Finished,
						Node:      name,
						AttemptID: n.Attempt.ID,
						Status:    Interrupted,
						Failure: &Failure{
							Code:    schema.InterruptedFailure,
							Message: "Owner restarted; execution outcome is unknown. Retry explicitly.",
						},
					})
				}

				if err := b.appendEvents(InterruptedKind, "Owner restarted; no inference was repeated", time.Now(), events...); err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			cancel()
			return nil, err
		}
	}

	return r, nil
}

func (r *Runner) Read(ctx context.Context, resource string) (Workspace, error) {
	return r.store.Read(ctx, resource)
}

func (r *Runner) update(ctx context.Context, resource string, fn func(*Workspace) error) (Workspace, error) {
	for range 8 {
		w, err := r.store.Read(ctx, resource)
		if err != nil {
			return Workspace{}, err
		}

		out, err := r.store.Update(ctx, resource, uuid.NewString(), w.Revision, "runner", fn)
		if errors.Is(err, ErrConflict) {
			continue
		}

		return out, err
	}

	return Workspace{}, ErrConflict
}

func (r *Runner) Apply(ctx context.Context, principal, resource, operation string, revision uint64, cmd Command) (Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := resource + "/" + cmd.Branch
	if cmd.Action == CancelCommand {
		// Cancellation is an explicit command, recorded before signalling the
		// worker. A stale browser cannot cancel a newer configuration.
		w, err := r.store.Update(ctx, resource, operation, revision, cmd, func(w *Workspace) error {
			b, err := w.Branch(cmd.Branch)
			if err != nil {
				return err
			}

			return b.appendEvents(CancellingKind, "Cancellation requested; waiting for the backend", time.Now())
		})
		if err == nil && r.runs[key] != nil {
			r.runs[key].cancel()
		}

		return w, err
	}

	w, err := r.store.Update(ctx, resource, operation, revision, cmd, func(w *Workspace) error {
		if r.runs[key] != nil && cmd.Action != ForkCommand && cmd.Action != RetryCommand {
			return errors.New("execution already active; cancel before editing")
		}

		now := time.Now()
		switch cmd.Action {
		case CreateCommand:
			if revision != 0 {
				return errors.New("pipeline already exists")
			}

			g := DefaultGraph()
			if cmd.Graph != nil {
				g = *cmd.Graph
			}

			if err := g.Validate(); err != nil {
				return err
			}
			*w = New(cmd.Name, g, now)

		case ConfigureCommand:
			if cmd.Graph == nil {
				return errors.New("graph is required")
			}

			return w.Configure(cmd.Branch, *cmd.Graph, now)

		case SelectCommand:
			return w.Select(cmd.Branch, cmd.Node, cmd.Index, cmd.Text, now)

		case ForkCommand:
			_, err := w.Fork(cmd.Branch, cmd.Checkpoint, now)
			return err

		case RetryCommand:
			_, err := w.Retry(cmd.Branch, cmd.Node, now)
			return err

		case RunNodeCommand, RunAllCommand, ContinueCommand:
			if r.executor == nil {
				return errors.New("inference service unavailable")
			}

			var err error
			w.ResultBranch = cmd.Branch
			switch cmd.Action {
			case RunNodeCommand:
				w.ResultBranch, err = w.RunNode(cmd.Branch, cmd.Node, now)
			case RunAllCommand:
				w.ResultBranch, err = w.RunAll(cmd.Branch, now)
			case ContinueCommand:
				err = w.Continue(cmd.Branch, now)
			}

			return err

		case RunCommand:
			if r.executor == nil {
				return errors.New("inference service unavailable")
			}
			if r.runs[key] != nil {
				return errors.New("execution already active")
			}

			_, err := w.Begin(cmd.Branch, now)
			return err

		default:
			return errors.New("unknown pipeline command")
		}

		return nil
	})
	if err != nil {
		return Workspace{}, err
	}

	branch := cmd.Branch
	workbook := cmd.Action == RunNodeCommand || cmd.Action == RunAllCommand || cmd.Action == ContinueCommand
	if workbook {
		branch = w.ResultBranch
	}

	key = resource + "/" + branch
	if (cmd.Action == RunCommand || workbook) && r.runs[key] == nil {
		// A replayed committed command must not start its attempt again. Read
		// the durable current state, not the historical receipt returned above.
		current, err := r.store.Read(ctx, resource)
		if err != nil {
			return Workspace{}, err
		}

		b, err := current.Branch(branch)
		if err != nil {
			return Workspace{}, err
		}
		if !b.Current().Busy() {
			return w, nil
		}

		receipt, err := w.Branch(branch)
		if err != nil {
			return Workspace{}, err
		}

		matches := false
		for id, n := range receipt.Current().Nodes {
			live := b.Current().Nodes[id]
			if n.Status() == Running && n.Attempt != nil && live.Status() == Running && live.Attempt != nil && n.Attempt.ID == live.Attempt.ID {
				matches = true
			}
		}
		if !matches {
			return w, nil
		}

		runCtx, cancel := context.WithCancel(r.ctx)
		job := &activeRun{cancel: cancel, done: make(chan struct{})}
		r.runs[key] = job
		continuous := (cmd.Action == RunCommand && cmd.Continue) || cmd.Action == RunAllCommand || cmd.Action == ContinueCommand
		go r.execute(runCtx, principal, resource, branch, key, continuous, job)
	}

	return w, nil
}

func (r *Runner) execute(ctx context.Context, principal, resource, branch, key string, continuous bool, job *activeRun) {
	defer func() {
		job.cancel()

		r.mu.Lock()
		delete(r.runs, key)
		close(job.done)
		r.mu.Unlock()
	}()

	for {
		w, err := r.store.Read(ctx, resource)
		if err != nil {
			return
		}

		b, err := w.Branch(branch)
		if err != nil {
			return
		}

		var attempt *Attempt
		for _, n := range b.Current().Nodes {
			if n.Status() == Running {
				attempt = n.Attempt
				break
			}
		}
		if attempt == nil {
			return
		}

		var (
			last       time.Time
			progressMu sync.Mutex
		)
		progress := func(message string) {
			progressMu.Lock()
			defer progressMu.Unlock()

			if time.Since(last) < 500*time.Millisecond {
				return
			}

			last = time.Now()

			if len(message) > 8192 {
				message = message[:8192]
			}

			_, _ = r.update(ctx, resource, func(w *Workspace) error {
				return w.Progress(branch, attempt.ID, attempt.ID, message, time.Now())
			})
		}

		result, runErr := r.executor.Execute(ctx, principal, resource, attempt.Arguments, progress)

		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		completed, persistErr := r.update(persistCtx, resource, func(w *Workspace) error {
			return w.Complete(branch, attempt.ID, result.Outputs, result.Explanation, result.Diagnostics, runErr, time.Now())
		})
		cancel()
		if persistErr != nil || runErr != nil || !continuous || ctx.Err() != nil {
			return
		}

		finished, _ := completed.Branch(branch)
		if finished.Current().Nodes[attempt.Arguments.Node.ID].Status() != Succeeded {
			return
		}

		_, err = r.update(ctx, resource, func(w *Workspace) error {
			b, err := w.Branch(branch)
			if err != nil {
				return err
			}

			if b.Current().RunAll {
				return w.Continue(branch, time.Now())
			}

			_, err = w.Begin(branch, time.Now())
			return err
		})
		if err != nil {
			return
		}
	}
}

func (r *Runner) Close(ctx context.Context) error {
	r.cancel()

	var jobs []*activeRun
	r.mu.Lock()
	for _, j := range r.runs {
		jobs = append(jobs, j)
	}
	r.mu.Unlock()

	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
