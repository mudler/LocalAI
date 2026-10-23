// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// EnableUpdates attaches the owner's update check. It reads the journal and
// the libraries the manager already holds, bounded by the configuration. It is
// owner-only and set once, before commands are served; Host defaults to the
// installer's.
func (m *Manager) EnableUpdates(cfg updates.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installer != nil && cfg.Host == (contract.HostCompatibility{}) {
		cfg.Host = m.installer.Host
	}
	m.updates = updates.New(cfg, updates.FromCatalog(m.catalog), &updateReader{m: m})
}

// updateReader is the manager as the checker reads it. It takes the lock for
// one lookup and gives up when the request is gone, so a status read does not
// wait behind a long lifecycle command without bound.
type updateReader struct{ m *Manager }

func (r *updateReader) Installation(ctx context.Context, id string) (updates.Installation, bool, error) {
	m := r.m
	for !m.mu.TryLock() {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return updates.Installation{}, false, ctx.Err()
		case <-timer.C:
		}
	}
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return updates.Installation{}, false, e
	}
	st, ok := m.journal.Addons[id]
	if !ok || !st.Installed {
		return updates.Installation{}, false, nil
	}
	receipt, ok := currentReceipt(st)
	if !ok {
		return updates.Installation{}, false, nil
	}
	return updates.Installation{AddonID: id, Generation: st.Generation, Receipt: receipt}, true, nil
}

// checkPins validates an explicit upgrade at execution. The caller holds the
// lock and nothing has been recorded: every refusal here leaves the journal as
// it was. It returns the release the library resolves to now, which is the
// candidate the caller pinned.
func (m *Manager) checkPins(ctx context.Context, cmd LifecycleCommand, st Status) (catalog.Release, error) {
	if m.updates == nil {
		return catalog.Release{}, catalog.ErrUnavailable
	}
	receipt, ok := currentReceipt(st)
	if !ok {
		return catalog.Release{}, ErrConflict
	}
	if receipt.Digest != cmd.ExpectedDigest || (cmd.Generation != 0 && cmd.Generation != st.Generation) {
		return catalog.Release{}, ErrStaleInstalled
	}
	res := m.updates.Evaluate(ctx, updates.Installation{AddonID: cmd.AddonID, Generation: st.Generation, Receipt: receipt})
	switch res.Status.State {
	case updates.IntegrityError:
		return catalog.Release{}, ErrIntegrity
	case updates.Incompatible:
		return catalog.Release{}, ErrIncompatible
	case updates.UpdateAvailable:
		if res.Status.CandidateDigest != cmd.CandidateDigest || res.Release.Digest != cmd.CandidateDigest {
			return catalog.Release{}, ErrCandidateChanged
		}
		// Compatibility is judged on the release as resolved now.
		if len(updates.Compatibility(m.installer.Host, receipt, res.Release)) > 0 {
			return catalog.Release{}, ErrIncompatible
		}
		return res.Release, nil
	case updates.UpToDate:
		return catalog.Release{}, ErrCandidateChanged
	}
	return catalog.Release{}, ErrCheckFailed
}

// visibleTo reports whether the actor may read update status of an add-on: an
// administrator for any installed add-on, anyone else only for an enabled one.
// Hiding is the same denial for an add-on that does not exist.
func (m *Manager) visibleTo(ctx context.Context, actor contract.Actor, id string) (admin bool, e error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e = m.check(ctx); e != nil {
		return false, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return false, ErrRecoveryRequired
	}
	admin = m.admin != nil && m.admin(ctx, actor) == nil
	st, ok := m.journal.Addons[id]
	if !addonID.MatchString(id) || !ok || !st.Installed || (!st.DesiredEnabled && !admin) {
		return false, ErrForbidden
	}
	return admin, nil
}

// UpdateStatus is the update check result of one add-on as the actor may see
// it. It never uses the network: it serves the cached answer or evaluates the
// library data the owner already holds. A caller who is not an administrator
// gets the state, versions and times only.
func (m *Manager) UpdateStatus(ctx context.Context, actor contract.Actor, id string) (updates.Status, error) {
	admin, e := m.visibleTo(ctx, actor, id)
	if e != nil {
		return updates.Status{}, e
	}
	if m.updates == nil {
		return updates.Status{}, catalog.ErrUnavailable
	}
	st, e := m.updates.Check(ctx, id, false)
	if e != nil {
		return updates.Status{}, classifyUpdateError(e)
	}
	if !admin {
		st = st.Redacted()
	}
	return st, nil
}

// CheckUpdates fetches the library of an installed add-on again and returns
// the new status. It is administrator-only, rate limited per add-on and
// library, and it applies nothing.
func (m *Manager) CheckUpdates(ctx context.Context, actor contract.Actor, id string) (updates.Status, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return updates.Status{}, e
	}
	if !addonID.MatchString(id) {
		return updates.Status{}, ErrForbidden
	}
	if m.updates == nil {
		return updates.Status{}, catalog.ErrUnavailable
	}
	st, e := m.updates.Check(ctx, id, true)
	if e != nil {
		return updates.Status{}, classifyUpdateError(e)
	}
	return st, nil
}

func classifyUpdateError(e error) error {
	if errors.Is(e, updates.ErrNotInstalled) {
		return ErrForbidden
	}
	return e
}

// updateView is the management view of a status.
func updateView(st updates.Status) *apps.Update {
	u := &apps.Update{Available: st.State == updates.UpdateAvailable, State: string(st.State), Version: st.CandidateVersion, Digest: st.CandidateDigest,
		Stale: st.Stale, Reasons: st.Reasons, Error: st.Error}
	if !st.CheckedAt.IsZero() {
		u.CheckedAt = st.CheckedAt.UTC().Format(time.RFC3339)
	}
	if !st.LastSuccess.IsZero() {
		u.LastSuccess = st.LastSuccess.UTC().Format(time.RFC3339)
	}
	return u
}
