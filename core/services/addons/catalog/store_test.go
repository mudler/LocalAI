package catalog

import (
	"path/filepath"
	"testing"
)

func openStore(t *testing.T, path string) *Sources {
	t.Helper()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func find(s *Sources, id string) (Source, bool) {
	for _, e := range s.Entries() {
		if e.ID == id {
			return e, true
		}
	}
	return Source{}, false
}

// A removed default survives a restart: the next SeedDefaults does not add it.
func TestTombstoneHoldsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons", "sources.json")
	s := openStore(t, path)
	if added, err := s.SeedDefaults(OfficialLibrary()); err != nil || !added {
		t.Fatalf("first seed: %v, %v", added, err)
	}
	if err := s.RemoveSource(OfficialLibraryID); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	if added, err := s.SeedDefaults(OfficialLibrary()); err != nil || added {
		t.Fatalf("second seed added the removed default: %v, %v", added, err)
	}
	e, ok := find(s, OfficialLibraryID)
	if !ok || !e.IsTombstone() || e.Enabled {
		t.Fatalf("record = %+v, %v", e, ok)
	}
}

// A new shipped default reaches an existing store, and a user source is kept.
func TestSeedIsAdditive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons", "sources.json")
	s := openStore(t, path)
	if _, err := s.SeedDefaults(OfficialLibrary()); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUser(Source{ID: "mine", Name: "Mine", URL: "https://example.org/i.yaml", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	if added, err := s.SeedDefaults(OfficialLibrary(), OfficialBackendLibrary()); err != nil || !added {
		t.Fatalf("seed: %v, %v", added, err)
	}
	for _, id := range []string{OfficialLibraryID, OfficialBackendLibraryID, "mine"} {
		if _, ok := find(s, id); !ok {
			t.Errorf("%s is missing", id)
		}
	}
}

// Saving through the management API never marks a source as a default.
func TestSaveCannotClaimDefault(t *testing.T) {
	s, err := NewSources(filepath.Join(t.TempDir(), "sources.json"), checker, CredentialResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Save(ctx, admin, Source{ID: "mine", Name: "Mine", URL: "https://example.org/i.yaml", Enabled: true, Default: true, Format: FormatGallery, Kind: KindModel}); e != nil {
		t.Fatal(e)
	}
	got, e := s.Get(ctx, admin, "mine")
	if e != nil || got.Default || got.Format != "" {
		t.Fatalf("got %+v, %v", got, e)
	}
}
