package catalog

import (
	"strings"
	"testing"
)

func TestOfficialSwitchSuspendsEveryShippedDefault(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables", "notes")
	f.user("mine", "extra")
	if got := strings.Join(f.listed(), ","); got != "mine/extra,official/notes,official/tables" {
		t.Fatalf("on: listed %s", got)
	}

	if !f.s.SetOfficial(false) {
		t.Fatal("turning the switch off changed nothing")
	}
	if got := strings.Join(f.listed(), ","); got != "mine/extra" {
		t.Errorf("off: the shipped default is still read: %s", got)
	}
	for _, e := range f.s.Entries() {
		if e.ID == "official" && (!e.Default || !e.Enabled || !e.Suspended) {
			t.Errorf("off: the default must stay in the store, enabled by its own state and suspended: %+v", e)
		}
		if e.ID == "mine" && e.Suspended {
			t.Error("off: a source of the user was suspended")
		}
	}

	f.s.SetOfficial(true)
	f.refresh("official")
	if got := strings.Join(f.listed(), ","); got != "mine/extra,official/notes,official/tables" {
		t.Errorf("on again: the default did not come back: %s", got)
	}
}

func TestOfficialSwitchKeepsADisabledDefaultDisabled(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables")
	if err := f.s.SetEnabled("official", false); err != nil {
		t.Fatal(err)
	}
	f.s.SetOfficial(false)
	f.s.SetOfficial(true)
	for _, e := range f.s.Entries() {
		if e.ID == "official" && (e.Enabled || e.Suspended) {
			t.Errorf("the switch changed the state the user chose: %+v", e)
		}
	}
}

func TestOfficialSwitchSuspendsADefaultSeededLater(t *testing.T) {
	f := newFixture(t)
	f.s.SetOfficial(false)
	if _, err := f.s.SeedDefaults(Source{ID: "official", Name: "official", URL: index(t, "tables"), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, e := range f.s.Entries() {
		if e.ID == "official" && !e.Suspended {
			t.Error("a default seeded while the switch is off must be suspended")
		}
	}
}

func TestConflictsNameTheSourcesThatClash(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables", "notes")
	f.user("mine", "tables")
	f.user("yours", "tables")
	got := f.s.Conflicts()
	for _, id := range []string{"mine", "yours"} {
		if !strings.Contains(got[id], `"mine"`) || !strings.Contains(got[id], `"yours"`) || !strings.Contains(got[id], "two of your library sources") {
			t.Errorf("%s: conflict = %q", id, got[id])
		}
	}
	if _, ok := got["official"]; ok {
		t.Errorf("a default that loses to two user sources is not the one in conflict, got %q", got["official"])
	}
}

func TestConflictsNoteADefaultThatLoses(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables", "notes")
	f.user("mine", "tables")
	got := f.s.Conflicts()
	if !strings.Contains(got["official"], "provided by your source \"mine\"") {
		t.Errorf("official: %q", got["official"])
	}
	if got["mine"] != "" {
		t.Errorf("the winning source has no conflict, got %q", got["mine"])
	}
}
