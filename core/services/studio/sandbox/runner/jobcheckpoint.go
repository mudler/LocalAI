package runner

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// CheckpointJob commits what a background job wrote in the sandbox of a user
// and a conversation as one change set with the title, under the entry lock,
// so it never runs inside a foreground call. The owner of the job calls it
// once the process has ended and before it calls release, so the entry is
// still counted as busy. A sandbox that is gone or was made again has nothing
// left to commit: that is not an error. The context gets the request (the
// add-on's store names the host operation of the job from it).
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
	_, _, err := e.sb.Checkpoint(ctx, title)
	if errors.Is(err, workspace.ErrCommitted) {
		return nil
	}
	return err
}
