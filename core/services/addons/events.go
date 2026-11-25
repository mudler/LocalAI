// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// EventSource is an installed add-on that declares events, as the watches
// need it: its ID, its title and the declarations of its current package.
type EventSource struct {
	AddonID string
	Title   contract.Localized
	Events  []contract.EventDeclaration
}

// EventSources lists the installed, enabled add-ons whose current package
// declares events, sorted by ID. A package the host would not admit is left
// out, as everywhere else.
func (m *Manager) EventSources(ctx context.Context) ([]EventSource, error) {
	v, e := m.readStateLive(ctx)
	if e != nil {
		return nil, e
	}
	var out []EventSource
	for id, st := range v.addons {
		if !st.Installed || !st.DesiredEnabled {
			continue
		}
		r, ok := currentReceipt(st)
		if !ok || len(r.Manifest.Capabilities.Events) == 0 || m.admit(r) != nil {
			continue
		}
		src := EventSource{AddonID: id, Title: contract.Localized{Text: id}, Events: slices.Clone(r.Manifest.Capabilities.Events)}
		if u := r.Manifest.Capabilities.UI; u != nil && u.Title != nil && u.Title.Default() != "" {
			src.Title = *u.Title
		}
		out = append(out, src)
	}
	slices.SortFunc(out, func(a, b EventSource) int { return strings.Compare(a.AddonID, b.AddonID) })
	return out, nil
}

// DeclaredEvent returns the declaration of an event type of an installed,
// enabled add-on, and false when the add-on does not declare it now.
func (m *Manager) DeclaredEvent(ctx context.Context, addonID, typ string) (contract.EventDeclaration, bool, error) {
	srcs, err := m.EventSources(ctx)
	if err != nil {
		return contract.EventDeclaration{}, false, err
	}
	for _, s := range srcs {
		if s.AddonID == addonID {
			d, ok := contract.Capabilities{Events: s.Events}.FindEvent(typ)
			return d, ok, nil
		}
	}
	return contract.EventDeclaration{}, false, nil
}
