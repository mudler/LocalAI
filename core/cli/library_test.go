package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

func libFlags(t *testing.T, dir string) LibraryFlags {
	t.Helper()
	return LibraryFlags{DataPath: dir, AddonsOfficialLibrary: true}
}

func entry(t *testing.T, f LibraryFlags, id string) (catalog.Source, bool) {
	t.Helper()
	store, err := f.open()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range store.Entries() {
		if e.ID == id {
			return e, true
		}
	}
	return catalog.Source{}, false
}

func TestLibraryListShowsDefaultsOfEveryKind(t *testing.T) {
	f := libFlags(t, t.TempDir())
	store, err := f.open()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeSources(&out, store.Entries()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"KIND", "ENABLED", "DEFAULT", "REMOVED", "addon", "official", "backend", "localai-backends", "model", "localai"} {
		if !strings.Contains(got, want) {
			t.Errorf("list is missing %q:\n%s", want, got)
		}
	}
	// A master build enables the model library and ships the localai gallery
	// off, so every model is listed once.
	for id, enabled := range map[string]bool{"official": true, "official-models": library.UseModelLibrary(), "localai": !library.UseModelLibrary(), "localai-backends": true} {
		e, ok := entry(t, f, id)
		if !ok || !e.Default || e.Enabled != enabled || e.Removed {
			t.Errorf("default %s = %+v, %v, want enabled=%v", id, e, ok, enabled)
		}
	}
}

func TestLibraryAddKinds(t *testing.T) {
	f := libFlags(t, t.TempDir())
	for _, kind := range []string{"addon", "backend", "model"} {
		c := LibraryAdd{URL: "https://example.org/" + kind + ".yaml", Kind: kind, ID: "mine-" + kind, Name: "Mine " + kind, Format: "library", LibraryFlags: f}
		if err := c.Run(nil); err != nil {
			t.Fatalf("add %s: %v", kind, err)
		}
		e, ok := entry(t, f, "mine-"+kind)
		if !ok || e.Default || !e.Enabled || string(e.Kind) != kind || e.Name != "Mine "+kind {
			t.Errorf("%s source = %+v, %v", kind, e, ok)
		}
	}
	// The same URL and kind twice, and a taken id, are refused.
	if err := (&LibraryAdd{URL: "https://example.org/addon.yaml", Kind: "addon", ID: "other", LibraryFlags: f, Format: "library"}).Run(nil); err == nil {
		t.Error("the same url was added twice")
	}
	if err := (&LibraryAdd{URL: "https://example.org/x.yaml", Kind: "addon", ID: "mine-addon", LibraryFlags: f, Format: "library"}).Run(nil); err == nil {
		t.Error("a taken id was accepted")
	}
	if err := (&LibraryAdd{URL: "not a url", Kind: "addon", LibraryFlags: f, Format: "library"}).Run(nil); err == nil {
		t.Error("a bad url was accepted")
	}
}

func TestLibraryAddDerivesTheID(t *testing.T) {
	f := libFlags(t, t.TempDir())
	if err := (&LibraryAdd{URL: "https://example.org/lib.yaml", Kind: "model", Name: "My Models", Format: "library", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	if e, ok := entry(t, f, "my-models"); !ok || e.Name != "My Models" {
		t.Errorf("derived id: %+v, %v", e, ok)
	}
}

func TestLibraryRemoveLeavesATombstoneForADefault(t *testing.T) {
	dir := t.TempDir()
	f := libFlags(t, dir)
	if err := (&LibraryRemove{ID: "official", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	// A new open stands for the next start: the default must not come back.
	e, ok := entry(t, f, "official")
	if !ok || !e.Removed || e.Enabled || !e.Default {
		t.Fatalf("after remove: %+v, %v", e, ok)
	}
	// Removing it again finds nothing to remove.
	if err := (&LibraryRemove{ID: "official", LibraryFlags: f}).Run(nil); err == nil {
		t.Error("a removed source was removed twice")
	}
	// Enable brings it back.
	if err := (&LibraryEnable{ID: "official", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	if e, _ := entry(t, f, "official"); e.Removed || !e.Enabled {
		t.Errorf("after enable: %+v", e)
	}
}

func TestLibraryRemoveDeletesAUserSource(t *testing.T) {
	f := libFlags(t, t.TempDir())
	if err := (&LibraryAdd{URL: "https://example.org/a.yaml", Kind: "addon", ID: "mine", Format: "library", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	if err := (&LibraryRemove{ID: "mine", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := entry(t, f, "mine"); ok {
		t.Error("a removed user source left a record")
	}
}

func TestLibraryEnableAndDisable(t *testing.T) {
	f := libFlags(t, t.TempDir())
	if err := (&LibraryDisable{ID: "localai", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	if e, _ := entry(t, f, "localai"); e.Enabled || e.Removed {
		t.Errorf("after disable: %+v", e)
	}
	if err := (&LibraryEnable{ID: "localai", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	if e, _ := entry(t, f, "localai"); !e.Enabled {
		t.Errorf("after enable: %+v", e)
	}
	if err := (&LibraryEnable{ID: "nope", LibraryFlags: f}).Run(nil); err == nil || !strings.Contains(err.Error(), "no source") {
		t.Errorf("unknown id: %v", err)
	}
}

// The store file is what the server reads: a second open sees the same state.
func TestLibraryStateIsSharedThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	f := libFlags(t, dir)
	if err := (&LibraryAdd{URL: "https://example.org/a.yaml", Kind: "backend", ID: "mine", Format: "library", LibraryFlags: f}).Run(nil); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.OpenStore(catalog.StorePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range store.Entries() {
		found = found || e.ID == "mine"
	}
	if !found {
		t.Error("the server store does not see the source")
	}
	_ = library.Defaults
}

func TestLibraryAddPinsTheSigner(t *testing.T) {
	f := libFlags(t, t.TempDir())
	url := "oci://registry.example/fixture-addon/library:1"
	c := LibraryAdd{URL: url, Kind: "addon", ID: "fixture", Format: "library", Issuer: "https://issuer.example", Identity: "signer@registry.example", LibraryFlags: f}
	if err := c.Run(nil); err != nil {
		t.Fatal(err)
	}
	e, ok := entry(t, f, "fixture")
	want := catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", Identity: "signer@registry.example"}
	if !ok || e.Policy != want {
		t.Errorf("policy = %+v, %v, want %+v", e.Policy, ok, want)
	}
	// One flag alone, and a policy on a source that is not an oci:// add-on
	// library, are refused.
	for _, bad := range []LibraryAdd{
		{URL: "oci://registry.example/b:1", Kind: "addon", ID: "b", Format: "library", Issuer: "https://issuer.example", LibraryFlags: f},
		{URL: "oci://registry.example/c:1", Kind: "addon", ID: "c", Format: "library", Identity: "signer@registry.example", LibraryFlags: f},
		{URL: "https://registry.example/index.yaml", Kind: "addon", ID: "d", Format: "library", Issuer: "https://issuer.example", Identity: "signer@registry.example", LibraryFlags: f},
	} {
		if err := bad.Run(nil); err == nil {
			t.Errorf("%+v was accepted", bad.URL)
		}
	}
}
