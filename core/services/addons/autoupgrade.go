// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	"github.com/mudler/xlog"
)

// AutoUpgradeActor is the actor the journal records for an upgrade the owner
// started on its own setting. It is not an account and cannot read operations.
var AutoUpgradeActor = contract.Actor{UserID: "system:auto-upgrade", CredentialID: "system:auto-upgrade", Mode: "system"} // #nosec G101 -- an actor label for system-started upgrades, not a credential

// AutoUpgradeResult is the outcome for one add-on in one pass.
type AutoUpgradeResult struct {
	AddonID          string
	From, To         string
	Upgraded         bool
	Err              error
	OperationID      string
	CandidateDigest  string
	SkippedAsFailure bool
	// HeldForApproval is set when the candidate needs host operations that
	// nobody approved. The old version keeps running; an administrator approves
	// the upgrade in the browser. An automatic upgrade never approves.
	HeldForApproval bool
}

// AutoUpgradeCandidate is an add-on the automatic upgrade would upgrade now.
type AutoUpgradeCandidate struct {
	AddonID         string
	From, To        string
	InstalledDigest string
	CandidateDigest string
}

// AutoUpgrade checks every installed add-on against its library, fetching the
// library again, and upgrades each one that has a newer release. It builds the
// same pinned upgrade command an administrator sends and runs it through the
// same Submit path, so the channel rules, the signer and digest checks, the
// SDK compatibility check and the backup before the upgrade all apply as they
// do for a manual upgrade. A refusal changes nothing and is logged. A candidate
// that failed once is not tried again until the library lists another digest,
// so a bad release does not fill the journal every cycle.
func (m *Manager) AutoUpgrade(ctx context.Context) []AutoUpgradeResult {
	checker, ids := m.autoIDs(ctx)
	if checker == nil {
		return nil
	}
	var out []AutoUpgradeResult
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if r, ok := m.autoUpgradeOne(ctx, checker, id); ok {
			out = append(out, r)
		}
	}
	return out
}

// AutoUpgradePending lists the add-ons the automatic upgrade would upgrade
// now, from a fresh check of their libraries, and changes nothing. An add-on
// whose candidate already failed in an earlier pass is left out, as
// AutoUpgrade skips it. An upgrade run uses it to know what to start.
func (m *Manager) AutoUpgradePending(ctx context.Context) []AutoUpgradeCandidate {
	checker, ids := m.autoIDs(ctx)
	if checker == nil {
		return nil
	}
	var out []AutoUpgradeCandidate
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		st, e := checker.Check(ctx, id, true)
		if e != nil || st.State != updates.UpdateAvailable || m.autoFailed(id, st.CandidateDigest) {
			continue
		}
		out = append(out, AutoUpgradeCandidate{AddonID: id, From: st.InstalledVersion, To: st.CandidateVersion,
			InstalledDigest: st.InstalledDigest, CandidateDigest: st.CandidateDigest})
	}
	return out
}

// AutoUpgradeStatuses is the update status of every installed add-on that
// has a library, read on the owner for the automatic upgrade run: the run
// lists what has an update and checks after an upgrade that the add-on moved.
// It has no actor and is never routed; only the process of the owner calls it.
// fresh fetches the libraries again. complete is false when an add-on could
// not be checked, so a missing one may still have an update.
func (m *Manager) AutoUpgradeStatuses(ctx context.Context, fresh bool) (out []updates.Status, complete bool, err error) {
	checker, ids := m.autoIDs(ctx)
	if checker == nil {
		return nil, false, catalog.ErrUnavailable
	}
	m.mu.Lock()
	ids = slices.DeleteFunc(ids, func(id string) bool {
		receipt, ok := currentReceipt(m.journal.Addons[id])
		return ok && receipt.SourceID == zipformat.SourceID
	})
	m.mu.Unlock()
	complete = true
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return out, false, e
		}
		st, e := checker.Check(ctx, id, fresh)
		if e != nil {
			complete = false
			continue
		}
		if st.State == updates.CheckFailed || st.State == updates.Unknown {
			complete = false
		}
		out = append(out, st)
	}
	return out, complete, nil
}

// AutoUpgradeOne upgrades one add-on as AutoUpgrade does, with the same
// rules: the owner's own actor, the pinned digests, no approval given on
// behalf of anybody (a candidate that needs one is held), and no new try of a
// candidate that failed. ok is false when the add-on has no update now.
func (m *Manager) AutoUpgradeOne(ctx context.Context, id string) (AutoUpgradeResult, bool) {
	m.mu.Lock()
	checker := m.updates
	m.mu.Unlock()
	if checker == nil {
		return AutoUpgradeResult{}, false
	}
	return m.autoUpgradeOne(ctx, checker, id)
}

// autoIDs returns the update checker and the installed add-ons, sorted.
func (m *Manager) autoIDs(ctx context.Context) (*updates.Checker, []string) {
	m.mu.Lock()
	checker := m.updates
	var ids []string
	if m.check(ctx) == nil {
		for id, st := range m.journal.Addons {
			if st.Installed {
				ids = append(ids, id)
			}
		}
	}
	m.mu.Unlock()
	if checker == nil {
		return nil, nil
	}
	sort.Strings(ids)
	return checker, ids
}

func (m *Manager) autoUpgradeOne(ctx context.Context, checker *updates.Checker, id string) (AutoUpgradeResult, bool) {
	st, e := checker.Check(ctx, id, true)
	if e != nil || st.State != updates.UpdateAvailable {
		return AutoUpgradeResult{}, false
	}
	r := AutoUpgradeResult{AddonID: id, From: st.InstalledVersion, To: st.CandidateVersion, CandidateDigest: st.CandidateDigest}
	if m.autoFailed(id, st.CandidateDigest) {
		r.SkippedAsFailure = true
		return r, true
	}
	r.OperationID = fmt.Sprintf("auto-%s-%d", id, time.Now().UnixNano())
	xlog.Info("Auto-upgrading add-on", "addon", id, "from", st.InstalledVersion, "to", st.CandidateVersion)
	_, r.Err = m.submit(ctx, AutoUpgradeActor, LifecycleCommand{
		OperationID: r.OperationID, AddonID: id, Action: "upgrade", Generation: st.Generation,
		ExpectedDigest: st.InstalledDigest, CandidateDigest: st.CandidateDigest,
	}, true)
	if r.Err != nil {
		var refusal *HostRequirementsRefusal
		r.HeldForApproval = errors.As(r.Err, &refusal) && !refusal.Blocked()
		if r.HeldForApproval {
			m.holdUpgrade(id, st.CandidateVersion, append(slices.Clone(refusal.Summary.MissingRequired), refusal.Summary.MissingOptional...))
		}
		m.rememberAutoFailure(id, st.CandidateDigest)
		xlog.Error("Failed to auto-upgrade add-on", "addon", id, "from", st.InstalledVersion, "to", st.CandidateVersion, "error", r.Err)
	} else {
		r.Upgraded = true
		xlog.Info("Add-on upgraded automatically", "addon", id, "version", st.CandidateVersion)
	}
	return r, true
}

func (m *Manager) autoFailed(id, digest string) bool {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return m.autoFailures[id] == digest
}

func (m *Manager) rememberAutoFailure(id, digest string) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	if m.autoFailures == nil {
		m.autoFailures = map[string]string{}
	}
	m.autoFailures[id] = digest
}
