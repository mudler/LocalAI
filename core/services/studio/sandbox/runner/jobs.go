package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

// The notes a call carries when background jobs change what it does.
const (
	// StaleNote ends the output of a call that ran on files that are older
	// than the user's, because a refresh would remove the work folder under
	// the jobs.
	StaleNote = "[files changed elsewhere are not seen here until your background jobs end]"
	// RemadeNote opens the output of a call that made the sandbox again and
	// so ended the jobs.
	RemadeNote = "[Your background jobs ended: the sandbox was made again because its access or environment changed.]"
)

// liveJob is one job counted on an entry.
type liveJob struct{ end bgjob.EndFunc }

// Launch starts the process of a job in sb. It runs with the entry locked and
// the sandbox ready, and returns the function that ends the job. The context
// of the Exec must not be the call's: it would kill the job when the call
// returns.
type Launch func(sb sandbox.Sandbox) (bgjob.EndFunc, error)

// StartJob runs launch against the sandbox of a user and a conversation and
// counts the job on the entry. While the count is above zero the entry is not
// evicted, suspended, refreshed or expired. A change of actor, view or profile
// ends the jobs (bgjob.EndSandboxRemade) and Close and Reset end them
// (bgjob.EndShutdown), through the EndFunc that launch returned.
//
// It returns release, which the owner of the job calls once the process has
// ended; it is safe to call more than once and after the runner ended the job.
// The runner does not checkpoint at the start.
func (m *Manager) StartJob(ctx context.Context, r Request, launch Launch) (release func(), err error) {
	if r.User == "" || r.Conversation == "" || r.Operation == "" {
		return nil, fmt.Errorf("%w: a job needs a user, a conversation and an operation", ErrInvalid)
	}
	if r.Call.ReadOnly {
		return nil, fmt.Errorf("%w: a read-only call cannot start a job", ErrInvalid)
	}
	if _, err := m.o.Backend(); err != nil {
		return nil, err
	}
	e, err := m.find(r.User, r.Conversation, false)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gone {
		return nil, ErrBusy
	}
	ctx = context.WithValue(ctx, requestKey{}, r)
	if m.o.Prepare != nil {
		ctx = m.o.Prepare(ctx, r)
	}
	if err := m.ready(ctx, e); err != nil {
		if e.sb == nil && e.snap == nil {
			m.forget(e)
		}
		return nil, err
	}
	if m.o.Begin != nil {
		end := m.o.Begin(ctx, r.User, r.Conversation, r.Operation)
		defer end()
	}
	end, err := launch(e.sb)
	if err != nil {
		return nil, err
	}
	if end == nil {
		return nil, errors.New("runner: a job needs a function that ends it")
	}
	j := &liveJob{end: end}
	m.mu.Lock()
	if e.jobs == nil {
		e.jobs = map[*liveJob]struct{}{}
	}
	e.jobs[j] = struct{}{}
	e.lastUse = m.o.Now()
	m.mu.Unlock()
	e.suspendFails, e.suspendAfter = 0, time.Time{}
	return func() {
		m.mu.Lock()
		if _, ok := e.jobs[j]; ok {
			delete(e.jobs, j)
			e.lastUse = m.o.Now() // the idle clock starts when the last job ends
		}
		m.mu.Unlock()
	}, nil
}

// LiveJobs is the number of live jobs of the sandbox of a user and a
// conversation.
func (m *Manager) LiveJobs(user, conv string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[key(user, conv)]; ok {
		return len(e.jobs)
	}
	return 0
}

// takeJobs removes the jobs of an entry from the count and returns their end
// functions.
func (m *Manager) takeJobs(e *entry) []bgjob.EndFunc {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(e.jobs) == 0 {
		return nil
	}
	out := make([]bgjob.EndFunc, 0, len(e.jobs))
	for j := range e.jobs {
		out = append(out, j.end)
	}
	e.jobs = nil
	return out
}

// endJobs ends every job of an entry for a reason, outside the manager's lock.
func (m *Manager) endJobs(e *entry, reason bgjob.EndReason) {
	for _, end := range m.takeJobs(e) {
		end(reason)
	}
}

// hasJobs reports whether the entry has a live job.
func (m *Manager) hasJobs(e *entry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(e.jobs) > 0
}
