package application

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
)

type fakeLibrary struct {
	updates   []backendlib.Update
	installed []string
}

func (f *fakeLibrary) Updates(context.Context) ([]backendlib.Update, error) { return f.updates, nil }
func (f *fakeLibrary) Install(_ context.Context, source, name, version string, _ backendlib.Progress) error {
	f.installed = append(f.installed, source+"/"+name+"@"+version)
	return nil
}

func TestLibraryBackendsFollowAutoUpgradeSwitch(t *testing.T) {
	for _, on := range []bool{true, false} {
		lib := &fakeLibrary{updates: []backendlib.Update{{Name: "whisper", SourceID: "official-backends", Version: "1.2.0"}}}
		uc := &UpgradeChecker{appConfig: &config.ApplicationConfig{AutoUpgradeBackends: on}}
		uc.SetLibraryBackends(func() libraryUpgrader { return lib })
		uc.upgradeLibraryBackends(context.Background())
		if on && len(lib.installed) != 1 {
			t.Fatalf("switch on: want one upgrade, got %v", lib.installed)
		}
		if on && lib.installed[0] != "official-backends/whisper@1.2.0" {
			t.Fatalf("wrong install %v", lib.installed)
		}
		if !on && len(lib.installed) != 0 {
			t.Fatalf("switch off: nothing may install, got %v", lib.installed)
		}
	}
}

func TestLibraryBackendsWithoutLibrary(t *testing.T) {
	uc := &UpgradeChecker{appConfig: &config.ApplicationConfig{AutoUpgradeBackends: true}}
	uc.SetLibraryBackends(func() libraryUpgrader { return nil })
	uc.upgradeLibraryBackends(context.Background())
}
