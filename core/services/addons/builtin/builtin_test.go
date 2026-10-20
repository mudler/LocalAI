// SPDX-License-Identifier: MIT
package builtin

import (
	"slices"
	"testing"
)

func ids(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestNoAddonIsBuiltIn(t *testing.T) {
	// Studio and Talk are packaged add-ons, installed and removed like the
	// others, so nothing is listed for a viewer, whatever the viewer holds.
	if got := ids(Visible(Viewer{Admin: true})); len(got) != 0 {
		t.Fatalf("no add-on is built in, got %v", got)
	}
	if got := ids(Visible(Viewer{Has: func(string) bool { return true }})); len(got) != 0 {
		t.Fatalf("no add-on is built in, got %v", got)
	}
	if len(IDs()) != 0 || len(Catalog()) != 0 {
		t.Fatalf("the catalog must be empty, got %v", IDs())
	}
}

func TestStudioAndTalkAreNotBuiltIn(t *testing.T) {
	for _, id := range []string{"studio", "talk"} {
		if slices.Contains(IDs(), id) {
			t.Fatalf("%s is a packaged add-on and must not be in the built-in catalog", id)
		}
	}
}

func TestGatesHideAnEntryFromAViewerWithoutTheFeature(t *testing.T) {
	gated := Entry{ID: "x", Feature: "fine_tuning"}
	adminOnly := Entry{ID: "y", AdminOnly: true}
	none := Viewer{Has: func(string) bool { return false }}
	if none.allows(gated) || none.allows(adminOnly) {
		t.Fatal("a viewer without the feature or the role must not see a gated entry")
	}
	has := Viewer{Has: func(f string) bool { return f == "fine_tuning" }}
	if !has.allows(gated) || has.allows(adminOnly) {
		t.Fatal("the feature opens a feature gate and nothing else")
	}
	admin := Viewer{Admin: true}
	if !admin.allows(gated) || !admin.allows(adminOnly) {
		t.Fatal("an administrator passes every gate")
	}
}

func TestIDsAreUniqueAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalog() {
		if seen[e.ID] || e.ID == "" || e.TitleKey == "" || e.MainPath == "" || e.Kind != KindPage {
			t.Fatalf("invalid or duplicate entry %+v", e)
		}
		seen[e.ID] = true
	}
}
