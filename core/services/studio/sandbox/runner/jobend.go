package runner

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// CheckpointJob saves what the background jobs of a sandbox wrote to the
// user's files, as one change set titled title. It runs under the entry's
// lock, as the checkpoint of a call does, so it never overlaps a call or a
// refresh. The owner of a job calls it once the job has ended. A sandbox that
// is gone (made again, closed, reset) has nothing to save: ok is false and
// the error is nil. A checkpoint that finds nothing to save is not an error.
func (m *Manager) CheckpointJob(ctx context.Context, user, conv, title string) (ok bool, conflicts []workspace.Conflict, err error) {
	m.mu.Lock()
	e := m.entries[key(user, conv)]
	m.mu.Unlock()
	if e == nil {
		return false, nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gone || e.sb == nil {
		return false, nil, nil
	}
	_, conflicts, err = e.sb.Checkpoint(ctx, m.o.Title+title)
	if errors.Is(err, workspace.ErrCommitted) {
		err = nil
	}
	m.mu.Lock()
	e.lastUse = m.o.Now()
	m.mu.Unlock()
	return err == nil, conflicts, err
}
