package library

import (
	"errors"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

func infoByID(list []Info, id string) (Info, bool) {
	for _, i := range list {
		if i.ID == id {
			return i, true
		}
	}
	return Info{}, false
}

func TestListShowsTheThreeKindsAndTheDefaults(t *testing.T) {
	store := open(t, t.TempDir())
	list := List(store)
	kinds := map[catalog.Kind]bool{}
	enabledModels := 0
	for _, i := range list {
		kinds[i.Kind] = true
		if !i.Default || i.Removed || i.Suspended {
			t.Errorf("%s: a fresh default is a default, not removed and not suspended: %+v", i.ID, i)
		}
		if i.Kind == catalog.KindModel && i.Enabled {
			enabledModels++
		}
		if i.Kind != catalog.KindModel && !i.Enabled {
			t.Errorf("%s: a default add-on or backend source starts enabled: %+v", i.ID, i)
		}
	}
	// Both model defaults are shipped, but a build reads one of them.
	if enabledModels != 1 {
		t.Errorf("%d model sources are enabled, want 1", enabledModels)
	}
	for _, k := range []catalog.Kind{catalog.KindAddon, catalog.KindBackend, catalog.KindModel} {
		if !kinds[k] {
			t.Errorf("no %s source listed", k)
		}
	}
}

func TestRemoveOfADefaultLeavesATombstoneAndAUserSourceIsDeleted(t *testing.T) {
	dir := t.TempDir()
	store := open(t, dir)
	if _, err := Add(store, catalog.KindModel, "https://example.org/m.yaml", "team", "Team", catalog.FormatGallery); err != nil {
		t.Fatal(err)
	}
	was, err := Remove(store, DefaultModelSourceID)
	if err != nil || !was {
		t.Fatalf("remove default = %v, %v", was, err)
	}
	was, err = Remove(store, "team")
	if err != nil || was {
		t.Fatalf("remove user source = %v, %v", was, err)
	}
	// The CLI and the routes read the same file: a new store sees the tombstone
	// and does not seed the default again.
	again := open(t, dir)
	list := List(again)
	d, ok := infoByID(list, DefaultModelSourceID)
	if !ok || !d.Removed || d.Enabled || !d.Default {
		t.Errorf("tombstone = %+v, %v", d, ok)
	}
	if _, ok := infoByID(list, "team"); ok {
		t.Error("a removed user source is still listed")
	}
	if _, err := Remove(again, "team"); !IsNotFound(err) {
		t.Errorf("removing it twice = %v", err)
	}
	// Enabling the tombstone brings the default back.
	if err := again.SetEnabled(DefaultModelSourceID, true); err != nil {
		t.Fatal(err)
	}
	if d, _ := infoByID(List(again), DefaultModelSourceID); d.Removed || !d.Enabled {
		t.Errorf("after enable: %+v", d)
	}
}

func TestAddRefusesTheSameKindAndURLTwice(t *testing.T) {
	store := open(t, t.TempDir())
	if _, err := Add(store, catalog.KindBackend, "https://example.org/b.yaml", "", "", catalog.FormatGallery); err != nil {
		t.Fatal(err)
	}
	_, err := Add(store, catalog.KindBackend, "https://example.org/b.yaml", "other", "", catalog.FormatGallery)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate = %v", err)
	}
}

func TestOfficialLibrariesOffHidesTheDefaultsAndKeepsTheUserSources(t *testing.T) {
	store := open(t, t.TempDir())
	models, backends := parse(config.DefaultGalleriesJSON), parse(config.DefaultBackendGalleriesJSON)
	team := config.Gallery{Name: "Team", URL: "https://example.org/models.yaml"}
	models = append(models, team)
	if _, err := MapGalleries(store, catalog.KindModel, models); err != nil {
		t.Fatal(err)
	}

	// On: everything is read.
	onM, onB := Reconcile(store, models, backends)
	if len(onM) != len(models) || len(onB) != len(backends) {
		t.Fatalf("on: models %v backends %v", onM, onB)
	}

	// Off: the shipped defaults are not read, the user's gallery is.
	store.SetOfficial(false)
	offM, offB := Reconcile(store, models, backends)
	if len(offM) != 1 || offM[0].URL != team.URL {
		t.Errorf("off: models = %+v", offM)
	}
	if len(offB) != 0 {
		t.Errorf("off: backends = %+v", offB)
	}
	if got := ApplyGalleries(store, catalog.KindModel, models); len(got) != 1 || got[0].URL != team.URL {
		t.Errorf("off: ApplyGalleries = %+v", got)
	}
	for _, i := range List(store) {
		if i.Default && (i.Enabled || !i.Suspended || i.Removed) {
			t.Errorf("off: default %s should be listed as off and suspended: %+v", i.ID, i)
		}
		if !i.Default && (!i.Enabled || i.Suspended) {
			t.Errorf("off: user source %s changed: %+v", i.ID, i)
		}
	}

	// On again: the lists come back from the store.
	store.SetOfficial(true)
	backM, backB := Reconcile(store, offM, offB)
	if len(backM) != len(models) || len(backB) != len(backends) {
		t.Errorf("on again: models %v backends %v", backM, backB)
	}
}

func TestReconcileKeepsAGalleryThatIsNotASource(t *testing.T) {
	store := open(t, t.TempDir())
	odd := []config.Gallery{{Name: "odd", URL: "https://example.org/odd.yaml"}}
	m, _ := Reconcile(store, odd, nil)
	if len(m) != len(odd)+1 { // plus the enabled default model source
		t.Errorf("models = %+v", m)
	}
	if m[0].URL != odd[0].URL {
		t.Errorf("order changed: %+v", m)
	}
}

func TestReconcileDropsTheGalleryOfADeletedSource(t *testing.T) {
	store := open(t, t.TempDir())
	team := config.Gallery{Name: "Team", URL: "https://example.org/team.yaml"}
	if _, err := Add(store, catalog.KindModel, team.URL, "team", "Team", catalog.FormatGallery); err != nil {
		t.Fatal(err)
	}
	current, _ := Reconcile(store, parse(config.DefaultGalleriesJSON), nil)
	if len(current) != 2 {
		t.Fatalf("setup: %+v", current)
	}
	if _, err := Remove(store, "team"); err != nil {
		t.Fatal(err)
	}
	// The store forgot the source, so without the address the gallery stays.
	if kept, _ := Reconcile(store, current, nil); len(kept) != 2 {
		t.Errorf("a gallery that is not a source is kept: %+v", kept)
	}
	if after, _ := Reconcile(store, current, nil, team.URL); len(after) != 1 || after[0].URL == team.URL {
		t.Errorf("the deleted source's gallery is still read: %+v", after)
	}
}
