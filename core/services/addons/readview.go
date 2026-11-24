// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"maps"
	"sort"

	"github.com/mudler/LocalAI/core/services/addons/apps"
)

// readView is an immutable copy of what the reads of the owner need: the
// installed add-ons, the operations and the flags that refuse a read.
//
// A lifecycle operation holds mu for most of its run (the backup, the commit,
// the start of the candidate, the restart). A read that waited for mu would
// wait for the whole upgrade, and so would every frontend that forwards a list
// to the owner. The journal replaces its values and never changes them in
// place, so a shallow copy of the two maps is a consistent snapshot. persist
// publishes a new one at every journal write, and a read that finds mu free
// refreshes it first, so an idle owner always answers from current state.
type readView struct {
	addons     map[string]Status
	operations map[string]journalOperation
	// owner is false after Close or when the storage lock is gone.
	owner bool
	// recovery is true while the owner needs a Reconcile or a restart.
	recovery bool
}

// refreshReadView publishes the current journal. The caller holds mu.
func (m *Manager) refreshReadView() {
	m.view.Store(&readView{
		addons:     maps.Clone(m.journal.Addons),
		operations: maps.Clone(m.journal.Operations),
		owner:      !m.closed && m.release != nil,
		recovery:   m.uncertain || m.runtimeRecoveryRequired,
	})
}

// readState returns the snapshot a read works on. It never waits for a
// lifecycle operation: when mu is held, the snapshot of the last journal write
// answers. It refuses like check does and, like the locked reads did, answers
// ErrRecoveryRequired while the owner recovers.
func (m *Manager) readState(ctx context.Context) (*readView, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if m.mu.TryLock() {
		m.refreshReadView()
		m.mu.Unlock()
	}
	v := m.view.Load()
	if v == nil {
		// Nothing was published yet (the owner is opening): wait for it.
		m.mu.Lock()
		m.refreshReadView()
		m.mu.Unlock()
		v = m.view.Load()
	}
	if !v.owner {
		return nil, ErrNotOwner
	}
	return v, nil
}

// readStateLive is readState for a read that also refuses while the owner
// recovers.
func (m *Manager) readStateLive(ctx context.Context) (*readView, error) {
	v, e := m.readState(ctx)
	if e != nil {
		return nil, e
	}
	if v.recovery {
		return nil, ErrRecoveryRequired
	}
	return v, nil
}

// operationFor is the operation that runs on an add-on in the snapshot, or nil.
// When several are active the oldest one is reported.
func (v *readView) operationFor(id string) *apps.Operation {
	var best *apps.Operation
	var bestID string
	for opID, op := range v.operations {
		if op.Command.AddonID != id || !isActive(op.Status.Phase) {
			continue
		}
		o := &apps.Operation{Kind: op.Command.Action, Phase: operationPhase(op), Since: op.StartedAt}
		if best == nil || o.Since < best.Since || (o.Since == best.Since && opID < bestID) {
			best, bestID = o, opID
		}
	}
	return best
}

// Stages the owner records while an operation runs. They are the durable
// words of the journal; operationPhase turns them into the few phases a page
// shows.
const (
	stagePreparing  = "preparing-package"
	stageCommitting = "committing-package"
	stageStarting   = "starting-candidate"
)

// operationPhase names the step of an active operation.
func operationPhase(op journalOperation) string {
	switch op.Status.Stage {
	case stagePreparing:
		return "downloading"
	case "backing-up":
		return "backing-up"
	case stageCommitting, "backed-up", "staged":
		return "installing"
	case "migrating":
		return "migrating"
	case stageStarting:
		return "starting"
	}
	if op.Status.Stage != "" {
		return "working"
	}
	if op.Status.Phase == string(lifecycleAccepted) {
		return "queued"
	}
	return "working"
}

// activeOperationIDs lists the IDs of the active operations, for tests and logs.
func (v *readView) activeOperationIDs() []string {
	var out []string
	for id, op := range v.operations {
		if isActive(op.Status.Phase) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
