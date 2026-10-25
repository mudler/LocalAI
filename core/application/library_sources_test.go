package application

import (
	"encoding/json"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/library"
)

// shipped sets the galleries a run with the default flags starts with.
func shipped(t *testing.T, cfg *config.ApplicationConfig) {
	t.Helper()
	if err := json.Unmarshal([]byte(config.DefaultGalleriesJSON), &cfg.Galleries); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(config.DefaultBackendGalleriesJSON), &cfg.BackendGalleries); err != nil {
		t.Fatal(err)
	}
}

func TestApplyLibrariesFollowsTheOfficialSwitchInBothStates(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewApplicationConfig(config.WithDataPath(dir))
	shipped(t, cfg)
	library.Option(dir, true, false, true)(cfg)
	wantModels, wantBackends := len(cfg.Galleries), len(cfg.BackendGalleries)
	if wantModels == 0 || wantBackends == 0 {
		t.Fatalf("the shipped galleries were not mapped: %d, %d", wantModels, wantBackends)
	}
	// A gallery of the user, so the off state has something left to read.
	store, err := library.Open(dir, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Add(store, "model", "https://example.org/team.yaml", "team", "Team", "gallery"); err != nil {
		t.Fatal(err)
	}
	a := &Application{applicationConfig: cfg}

	cfg.OfficialLibraries = false
	if err := a.ApplyLibraries(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Galleries) != 1 || cfg.Galleries[0].URL != "https://example.org/team.yaml" || len(cfg.BackendGalleries) != 0 {
		t.Errorf("off: models %+v backends %+v", cfg.Galleries, cfg.BackendGalleries)
	}

	cfg.OfficialLibraries = true
	if err := a.ApplyLibraries(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Galleries) != wantModels+1 || len(cfg.BackendGalleries) != wantBackends {
		t.Errorf("on: models %+v backends %+v", cfg.Galleries, cfg.BackendGalleries)
	}
}

func TestOfficialLibrariesOffAtStartupDropsTheDefaultGalleries(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewApplicationConfig(config.WithDataPath(dir))
	shipped(t, cfg)
	library.Option(dir, true, false, true)(cfg)

	cfg.OfficialLibraries = true
	applyOfficialLibrariesAtStartup(cfg)
	if len(cfg.Galleries) == 0 || len(cfg.BackendGalleries) == 0 {
		t.Fatal("on: the default galleries were dropped")
	}
	cfg.OfficialLibraries = false
	applyOfficialLibrariesAtStartup(cfg)
	if len(cfg.Galleries) != 0 || len(cfg.BackendGalleries) != 0 {
		t.Errorf("off: models %+v backends %+v", cfg.Galleries, cfg.BackendGalleries)
	}
}
