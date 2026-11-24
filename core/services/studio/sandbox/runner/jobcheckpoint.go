package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// EndFunc and EndReason are the job vocabulary of bgjob under the runner's
// name, so an add-on that runs jobs through the runner imports no other
// package of the core.
type (
	EndFunc   = bgjob.EndFunc
	EndReason = bgjob.EndReason
)

// ConflictsError says the final checkpoint of a job was saved, but some of
// its files conflict with newer changes made elsewhere. The caller reports
// it; nothing is lost that a later change set cannot correct.
type ConflictsError struct{ Conflicts []workspace.Conflict }

func (e *ConflictsError) Error() string {
	c := e.Conflicts[0]
	return fmt.Sprintf("%d files of the job conflict with newer changes (%s: %s)", len(e.Conflicts), c.Path, c.Reason)
}

// CheckpointJob commits what a background job wrote in the sandbox of a user
// and a conversation as one change set, titled with the runner's Title and
// title, under the entry lock, so it never runs inside a foreground call. The
// owner of the job calls it once the process has ended and before it calls
// release, so the entry is still counted as busy. A sandbox that is gone or
// was made again has nothing left to commit: that is not an error. The
// context gets the request (the add-on's store names the host operation of
// the job from it). Conflicts with newer changes are a *ConflictsError.
func (m *Manager) CheckpointJob(ctx context.Context, r Request, title string) error {
	if r.User == "" || r.Conversation == "" || r.Operation == "" {
		return ErrInvalid
	}
	m.mu.Lock()
	e := m.entries[key(r.User, r.Conversation)]
	m.mu.Unlock()
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gone || e.sb == nil {
		return nil
	}
	ctx = context.WithValue(ctx, requestKey{}, r)
	if m.o.Prepare != nil {
		ctx = m.o.Prepare(ctx, r)
	}
	_, conflicts, err := e.sb.Checkpoint(ctx, m.o.Title+title)
	m.mu.Lock()
	e.lastUse = m.o.Now()
	m.mu.Unlock()
	switch {
	case errors.Is(err, workspace.ErrCommitted):
		return nil
	case err != nil:
		return err
	case len(conflicts) > 0:
		return &ConflictsError{Conflicts: conflicts}
	}
	return nil
}
