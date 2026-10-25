// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
		return nil
	}
	sort.Strings(ids)
	var out []AutoUpgradeResult
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		st, e := checker.Check(ctx, id, true)
		if e != nil || st.State != updates.UpdateAvailable {
			continue
		}
		r := AutoUpgradeResult{AddonID: id, From: st.InstalledVersion, To: st.CandidateVersion, CandidateDigest: st.CandidateDigest}
		if m.autoFailed(id, st.CandidateDigest) {
			r.SkippedAsFailure = true
			out = append(out, r)
			continue
		}
		r.OperationID = fmt.Sprintf("auto-%s-%d", id, time.Now().UnixNano())
		xlog.Info("Auto-upgrading add-on", "addon", id, "from", st.InstalledVersion, "to", st.CandidateVersion)
		_, r.Err = m.submit(ctx, AutoUpgradeActor, LifecycleCommand{
			OperationID: r.OperationID, AddonID: id, Action: "upgrade", Generation: st.Generation,
			ExpectedDigest: st.InstalledDigest, CandidateDigest: st.CandidateDigest,
		}, true)
		if r.Err != nil {
			m.rememberAutoFailure(id, st.CandidateDigest)
			xlog.Error("Failed to auto-upgrade add-on", "addon", id, "from", st.InstalledVersion, "to", st.CandidateVersion, "error", r.Err)
		} else {
			r.Upgraded = true
			xlog.Info("Add-on upgraded automatically", "addon", id, "version", st.CandidateVersion)
		}
		out = append(out, r)
	}
	return out
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
