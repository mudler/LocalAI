// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

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
	v, e := m.readStateLive(ctx)
	if e != nil {
		return nil, e
	}
	var out []HostAccessInstalled
	for id, st := range v.addons {
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
		for oid, op := range v.operations {
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

// previewKeep is how long the verified answer for one release is reused. The
// key holds the digest the library pins, so a changed package is a new key;
// the time only bounds how long an answer outlives a withdrawn release.
const previewKeep = 10 * time.Minute

// previewMax bounds the answers kept.
const previewMax = 32

type previewKey struct{ source, addon, version, digest, artifact string }

type previewEntry struct {
	rel  HostAccessRelease
	at   time.Time
	done chan struct{}
	err  error
}

// previewCache keeps the answer of the staged, verified package of a release,
// and lets concurrent reads of one release share a single download. It holds
// only what the verified manifest says; whether the add-on is installed is
// read again on every call.
type previewCache struct {
	mu      sync.Mutex
	entries map[previewKey]*previewEntry
}

// PreviewRelease resolves and stages a release, reads its verified manifest
// and discards the stage. It changes no state of the add-ons. A release the
// library pins by digest is staged once: the digest fixes the bytes, so the
// verified answer holds for the same key, and a second read (a retry, the
// upgrade approval, another administrator) does not download it again.
func (m *Manager) PreviewRelease(ctx context.Context, sourceID, addonID, version string) (HostAccessRelease, error) {
	if m.installer == nil {
		return HostAccessRelease{}, ErrForbidden
	}
	m.mu.Lock()
	if e := m.check(ctx); e != nil {
		m.mu.Unlock()
		return HostAccessRelease{}, e
	}
	installed := m.journal.Addons[addonID].Installed
	m.mu.Unlock()
	if sourceID == zipformat.SourceID {
		// A local file: the version field names the staged file.
		m.opMu.Lock()
		defer m.opMu.Unlock()
		return m.previewFile(ctx, version)
	}
	r, e := m.resolveRelease(ctx, LifecycleCommand{SourceID: sourceID, AddonID: addonID, Version: version})
	if e != nil {
		return HostAccessRelease{}, e
	}
	var out HostAccessRelease
	if r.Mutable || r.Digest == "" {
		// A moving tag names no fixed bytes: always read it.
		out, e = m.stagePreview(ctx, r)
	} else {
		out, e = m.previews.get(ctx, previewKey{sourceID, addonID, version, r.Digest, r.Artifact}, func() (HostAccessRelease, error) { return m.stagePreview(ctx, r) })
	}
	if e != nil {
		return HostAccessRelease{}, e
	}
	out.Installed = installed
	return out, nil
}

func (c *previewCache) get(ctx context.Context, k previewKey, load func() (HostAccessRelease, error)) (HostAccessRelease, error) {
	for {
		c.mu.Lock()
		if c.entries == nil {
			c.entries = map[previewKey]*previewEntry{}
		}
		e, ok := c.entries[k]
		if ok {
			select {
			case <-e.done:
				if e.err == nil && time.Since(e.at) < previewKeep {
					c.mu.Unlock()
					return e.rel, nil
				}
				delete(c.entries, k)
				ok = false
			default:
			}
		}
		if !ok {
			e = &previewEntry{done: make(chan struct{})}
			c.entries[k] = e
			c.prune()
			c.mu.Unlock()
			e.rel, e.err = load()
			e.at = time.Now()
			c.mu.Lock()
			if e.err != nil && c.entries[k] == e {
				delete(c.entries, k)
			}
			c.mu.Unlock()
			close(e.done)
			return e.rel, e.err
		}
		c.mu.Unlock()
		select {
		case <-e.done:
			if e.err == nil {
				return e.rel, nil
			}
			// The reader ahead failed, maybe because its caller left: try on our own.
			if ctx.Err() != nil {
				return HostAccessRelease{}, ctx.Err()
			}
			if !errors.Is(e.err, context.Canceled) && !errors.Is(e.err, context.DeadlineExceeded) {
				return HostAccessRelease{}, e.err
			}
		case <-ctx.Done():
			return HostAccessRelease{}, ctx.Err()
		}
	}
}

// prune drops the oldest finished answers over the bound. The caller holds mu.
func (c *previewCache) prune() {
	for len(c.entries) > previewMax {
		var oldest previewKey
		var at time.Time
		found := false
		for k, e := range c.entries {
			select {
			case <-e.done:
			default:
				continue
			}
			if !found || e.at.Before(at) {
				oldest, at, found = k, e.at, true
			}
		}
		if !found {
			return
		}
		delete(c.entries, oldest)
	}
}

// stagePreview downloads and verifies the package of a release, reads its
// manifest and discards the stage. It holds the operation lock for the stage
// only, so the stage folder is not cleaned under it.
func (m *Manager) stagePreview(ctx context.Context, r catalog.Release) (HostAccessRelease, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	staged, e := m.installer.Stage(ctx, r, nil)
	if e != nil {
		return HostAccessRelease{}, e
	}
	rc := staged.Receipt()
	if e = m.installer.Discard(staged); e != nil {
		return HostAccessRelease{}, e
	}
	out := HostAccessRelease{AddonID: rc.Manifest.ID, Title: rc.Manifest.ID, Version: rc.Manifest.Version, Digest: rc.Digest,
		Official: OfficialReceipt(rc)}
	if u := rc.Manifest.Capabilities.UI; u != nil && u.Title != nil && u.Title.Default() != "" {
		out.Title = u.Title.Default()
	}
	out.Required, out.Optional = declared(rc)
	return out, nil
}
