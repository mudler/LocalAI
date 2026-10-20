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

func TestStudioAndTalkAreVisibleWithoutAnyFeature(t *testing.T) {
	got := ids(Visible(Viewer{Admin: false, Has: func(string) bool { return false }}))
	if !slices.Equal(got, []string{"studio", "talk"}) {
		t.Fatalf("a viewer with no feature must still see studio and talk, got %v", got)
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
