// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
)

// OfficialReceipt reports whether a package came from the official add-on
// library that LocalAI ships and carries the official signature. The source ID
// alone is not enough, since an administrator names libraries freely.
func OfficialReceipt(r packages.PackageReceipt) bool {
	return r.SourceID == catalog.OfficialLibraryID && r.Verification == "sha256+sigstore" && catalog.IsOfficialAddonURL(r.SourceURL)
}

// HostAccessInstalled is an installed add-on as the host access page needs it.
type HostAccessInstalled struct {
	AddonID, Title, Version, State, Identity, SourceID string
	Generation                                         uint64
	// OperationID is the lifecycle operation that completed the installed
	// generation.
	OperationID        string
	Official           bool
	Required, Optional []string
	// NewOptional are optional operations that the installed version asks for,
	// that the generation before did not, and that nothing grants yet.
	NewOptional []string
	// HeldVersion and HeldDelta describe an upgrade that waits for approval.
	HeldVersion string
	HeldDelta   []string
}

type heldUpgrade struct {
	version string
	delta   []string
}

func (m *Manager) holdUpgrade(id, version string, delta []string) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	if m.heldUpgrades == nil {
		m.heldUpgrades = map[string]heldUpgrade{}
	}
	m.heldUpgrades[id] = heldUpgrade{version: version, delta: slices.Clone(delta)}
}

func declared(r packages.PackageReceipt) (required, optional []string) {
	if h := r.Manifest.Capabilities.HostOperations; h != nil {
		return slices.Clone(h.Required), slices.Clone(h.Optional)
	}
	return nil, nil
}

// HostAccessInstalled lists the installed add-ons with the host operations
// their verified manifests ask for. It never reads a grant: the caller asks
// the Authority.
func (m *Manager) HostAccessInstalled(ctx context.Context) ([]HostAccessInstalled, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	var out []HostAccessInstalled
	for id, st := range m.journal.Addons {
		r, ok := currentReceipt(st)
		if !ok || !st.Installed {
			continue
		}
		in := HostAccessInstalled{AddonID: id, Title: id, Version: r.Manifest.Version, Identity: st.Identity, SourceID: r.SourceID,
			Generation: st.Generation, Official: OfficialReceipt(r), State: addonruntime.StateStopped}
		if u := r.Manifest.Capabilities.UI; u != nil && u.Title != nil && u.Title.Default() != "" {
			in.Title = u.Title.Default()
		}
		if st.DesiredEnabled && m.runtime != nil {
			in.State = m.runtime.State(id, st.Generation)
		}
		if m.admit(r) != nil {
			in.State = "failed"
		}
		in.Required, in.Optional = declared(r)
		for _, g := range st.Generations {
			if g.Number+1 == st.Generation {
				pr, po := declared(g.Receipt)
				for _, op := range in.Optional {
					if !slices.Contains(pr, op) && !slices.Contains(po, op) {
						if ok, _ := m.hostAuthority().Allows(id, op); !ok {
							in.NewOptional = append(in.NewOptional, op)
						}
					}
				}
			}
		}
		for oid, op := range m.journal.Operations {
			if op.Command.AddonID == id && op.Status.Phase == string(lifecycleCompleted) && op.Status.Generation == st.Generation &&
				(op.Command.Action == "install" || op.Command.Action == "upgrade") && (in.OperationID == "" || oid < in.OperationID) {
				in.OperationID = oid
			}
		}
		m.autoMu.Lock()
		if h, ok := m.heldUpgrades[id]; ok && h.version != in.Version {
			in.HeldVersion, in.HeldDelta = h.version, slices.Clone(h.delta)
		}
		m.autoMu.Unlock()
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddonID < out[j].AddonID })
	return out, nil
}

// HostAccessRelease is what the verified, staged package of a release asks for.
type HostAccessRelease struct {
	AddonID, Title, Version, Digest string
	Official, Installed             bool
	Required, Optional              []string
}

// PreviewRelease resolves and stages a release, reads its verified manifest
// and discards the stage. It changes no state of the add-ons.
func (m *Manager) PreviewRelease(ctx context.Context, sourceID, addonID, version string) (HostAccessRelease, error) {
	if m.installer == nil {
		return HostAccessRelease{}, ErrForbidden
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if e := m.check(ctx); e != nil {
		m.mu.Unlock()
		return HostAccessRelease{}, e
	}
	installed := m.journal.Addons[addonID].Installed
	m.mu.Unlock()
	if sourceID == zipformat.SourceID {
		// A local file: the version field names the staged file.
		return m.previewFile(ctx, version)
	}
	r, e := m.resolveRelease(ctx, LifecycleCommand{SourceID: sourceID, AddonID: addonID, Version: version})
	if e != nil {
		return HostAccessRelease{}, e
	}
	staged, e := m.installer.Stage(ctx, r, nil)
	if e != nil {
		return HostAccessRelease{}, e
	}
	rc := staged.Receipt()
	if e = m.installer.Discard(staged); e != nil {
		return HostAccessRelease{}, e
	}
	out := HostAccessRelease{AddonID: rc.Manifest.ID, Title: rc.Manifest.ID, Version: rc.Manifest.Version, Digest: rc.Digest,
		Official: OfficialReceipt(rc), Installed: installed}
	if u := rc.Manifest.Capabilities.UI; u != nil && u.Title != nil && u.Title.Default() != "" {
		out.Title = u.Title.Default()
	}
	out.Required, out.Optional = declared(rc)
	return out, nil
}
