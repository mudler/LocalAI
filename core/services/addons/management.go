// SPDX-License-Identifier: MIT
package addons

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Limits of the management reads.
const (
	maxSearchText  = 256
	maxVersionText = 64
)

// authorize admits only the current owner and an administrator the owner's own
// check confirms. Every management read and write passes it, so the browser
// surface never decides who may manage.
func (m *Manager) authorize(ctx context.Context, actor contract.Actor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return e
	}
	if m.admin == nil || m.admin(ctx, actor) != nil {
		return ErrForbidden
	}
	return nil
}

// CatalogSearch lists the library entries that match the owner's configured
// target platform, the same rule an install resolves with.
func (m *Manager) CatalogSearch(ctx context.Context, actor contract.Actor, q catalog.Query) (catalog.Page, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return catalog.Page{}, e
	}
	if len(q.Text) > maxSearchText || q.Offset < 0 || q.Limit < 0 || q.Limit > 100 {
		return catalog.Page{}, catalog.ErrInvalid
	}
	return m.catalog.Search(ctx, q)
}

// InstalledSummary is what a library entry shows of the addon's installed state.
type InstalledSummary struct {
	Version    string `json:"version"`
	Generation uint64 `json:"generation"`
	Enabled    bool   `json:"enabled"`
	// Update is the owner's update check of the installed add-on, when it can
	// answer for it. A library entry shows it next to the installed version.
	Update *apps.Update `json:"update,omitempty"`
}

// CatalogDetails is one resolved library entry and, when the addon is installed,
// the installed state beside it.
type CatalogDetails struct {
	Release   catalog.Release   `json:"release"`
	Installed *InstalledSummary `json:"installed"`
}

// CatalogDetails resolves one entry exactly as an install would.
func (m *Manager) CatalogDetails(ctx context.Context, actor contract.Actor, sourceID, id, version string) (CatalogDetails, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return CatalogDetails{}, e
	}
	if !addonID.MatchString(id) || !sourceIDPattern.MatchString(sourceID) || version == "" || len(version) > maxVersionText {
		return CatalogDetails{}, catalog.ErrInvalid
	}
	release, e := m.catalog.Resolve(ctx, sourceID, id, version)
	if e != nil {
		return CatalogDetails{}, e
	}
	out := CatalogDetails{Release: release}
	m.mu.Lock()
	if st, ok := m.journal.Addons[id]; ok && st.Installed {
		out.Installed = &InstalledSummary{Generation: st.Generation, Enabled: st.DesiredEnabled}
		if receipt, ok := currentReceipt(st); ok {
			out.Installed.Version = receipt.Manifest.Version
		}
	}
	m.mu.Unlock()
	if out.Installed != nil && m.updates != nil {
		if st, e := m.updates.Check(ctx, id, false); e == nil {
			out.Installed.Update = updateView(st)
		}
	}
	return out, nil
}

// Sources lists the configured libraries with the state of their last fetch.
func (m *Manager) Sources(ctx context.Context, actor contract.Actor) ([]catalog.SourceView, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return nil, e
	}
	if m.catalog.Sources == nil {
		return nil, catalog.ErrUnavailable
	}
	return m.catalog.Sources.Views(ctx, actor)
}

// SaveSource stores or removes a library. A library names a credential by a
// reference the owner configured and never carries a secret: any other
// reference is refused, so a pasted token is not stored or shown back. Removing
// a library never touches installed addons. A browser cannot newly turn on the
// plaintext loopback opt-in, which is an owner development switch.
func (m *Manager) SaveSource(ctx context.Context, actor contract.Actor, src catalog.Source) (catalog.Source, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return catalog.Source{}, e
	}
	if m.catalog.Sources == nil {
		return catalog.Source{}, catalog.ErrUnavailable
	}
	if src.Removed {
		src = catalog.Source{ID: src.ID, Name: src.Name, URL: src.URL, Removed: true}
	} else {
		if src.AuthReference != "" && !m.catalog.Sources.HasReference(src.AuthReference) {
			return catalog.Source{}, catalog.ErrInvalid
		}
		if src.AllowLoopbackHTTP {
			existing, e := m.catalog.Sources.Get(ctx, actor, src.ID)
			if e != nil || !existing.AllowLoopbackHTTP {
				return catalog.Source{}, catalog.ErrInvalid
			}
		}
	}
	if e := m.catalog.Sources.Save(ctx, actor, src); e != nil {
		return catalog.Source{}, e
	}
	return src, nil
}

// RefreshSource fetches one library again.
func (m *Manager) RefreshSource(ctx context.Context, actor contract.Actor, sourceID string) (catalog.RefreshResult, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return catalog.RefreshResult{}, e
	}
	if m.catalog.Sources == nil {
		return catalog.RefreshResult{}, catalog.ErrUnavailable
	}
	if !sourceIDPattern.MatchString(sourceID) {
		return catalog.RefreshResult{}, catalog.ErrInvalid
	}
	return m.catalog.Sources.Refresh(ctx, sourceID)
}

// Installed is the management view of every installed addon: verified package
// facts, desired state, the process observation, retained generations and the
// size of the retained data.
func (m *Manager) Installed(ctx context.Context, actor contract.Actor) ([]apps.Installed, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return nil, e
	}
	m.mu.Lock()
	if m.uncertain || m.runtimeRecoveryRequired {
		m.mu.Unlock()
		return nil, ErrRecoveryRequired
	}
	snaps := make([]apps.Snapshot, 0, len(m.journal.Addons))
	generations := map[string]int{}
	restarts := map[string]int{}
	dirs := map[string]string{}
	for id, st := range m.journal.Addons {
		receipt, ok := currentReceipt(st)
		if !ok || !st.Installed {
			continue
		}
		snap := apps.Snapshot{AddonID: id, Installed: true, Enabled: st.DesiredEnabled, Generation: st.Generation, State: addonruntime.StateStopped, Receipt: receipt}
		if st.DesiredEnabled && m.runtime != nil {
			snap.State = m.runtime.State(id, st.Generation)
		}
		if m.runtime != nil {
			restarts[id] = m.runtime.Attempts(id)
		}
		snaps = append(snaps, snap)
		generations[id] = len(st.Generations)
		dirs[id] = st.DataDirectory
	}
	m.mu.Unlock()
	list := apps.BuildInstalled(snaps)
	for i := range list {
		id := list[i].ID
		list[i].Generations = generations[id]
		list[i].Restarts = restarts[id]
		list[i].Storage = m.scanner.size(ctx, dirs[id])
		if m.recovery != nil {
			if st := m.recovery.StateOf(id); st != "ready" {
				list[i].Recovery = st
			}
		}
		// The owner's own check, from library data it already holds. An
		// add-on the checker cannot answer for has no update object, which
		// the page reads as unknown, never as current.
		if m.updates != nil {
			if st, e := m.updates.Check(ctx, id, false); e == nil {
				list[i].Update = updateView(st)
			}
		}
	}
	return list, ctx.Err()
}
