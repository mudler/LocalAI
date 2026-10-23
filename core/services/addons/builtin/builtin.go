// SPDX-License-Identifier: MIT

// Package builtin lists the page add-ons that ship inside the LocalAI web
// bundle. None does today: Studio, Talk, Voice, Face, fine-tuning and
// quantization are packaged add-ons that a person installs and removes, so the
// catalog is empty. The package stays because it is the one place that says
// who may see a built-in page, so the pin routes and the drawer agree on it the
// day one is added. Page add-ons have no process and no lifecycle of their own.
package builtin

import "slices"

// Kind says how an entry opens. Only pages exist: a core React page.
type Kind string

const KindPage Kind = "page"

// Entry is one built-in add-on as the browser draws it. TitleKey is a key of
// the `nav` i18n namespace and Icon is a host icon name.
type Entry struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	TitleKey  string `json:"titleKey"`
	Icon      string `json:"icon"`
	MainPath  string `json:"mainPath"`
	Feature   string `json:"feature,omitempty"`
	AdminOnly bool   `json:"adminOnly,omitempty"`
}

// Viewer is what the gates need to know about the caller.
type Viewer struct {
	Admin bool
	Has   func(feature string) bool
}

func (v Viewer) allows(e Entry) bool {
	if v.Admin {
		return true
	}
	if e.AdminOnly {
		return false
	}
	if e.Feature != "" && (v.Has == nil || !v.Has(e.Feature)) {
		return false
	}
	return true
}

var catalog = []Entry{}

// SetCatalogForTest replaces the catalog and returns a function that puts the
// old one back. No page is built in today, so the tests of the packages that
// read the catalog (the pin and list routes, the pin sweep) use it to check
// the rules against entries they make up. It is not safe for concurrent use.
func SetCatalogForTest(entries []Entry) (restore func()) {
	old := catalog
	catalog = slices.Clone(entries)
	return func() { catalog = old }
}

// Catalog returns a copy of every entry, in display order.
func Catalog() []Entry { return slices.Clone(catalog) }

// IDs returns the identifier of every entry.
func IDs() []string {
	out := make([]string, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e.ID)
	}
	return out
}

// Visible returns the entries the viewer passes the gates of.
func Visible(v Viewer) []Entry {
	out := []Entry{}
	for _, e := range catalog {
		if v.allows(e) {
			out = append(out, e)
		}
	}
	return out
}
