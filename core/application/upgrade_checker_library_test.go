package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

var _ = Describe("upgrading library backends", func() {
	It("follows the auto-upgrade switch", func() {
		for _, on := range []bool{true, false} {
			lib := &fakeLibrary{updates: []backendlib.Update{{Name: "whisper", SourceID: "official-backends", Version: "1.2.0"}}}
			uc := &UpgradeChecker{appConfig: &config.ApplicationConfig{AutoUpgradeBackends: on}}
			uc.SetLibraryBackends(func() libraryUpgrader { return lib })
			uc.upgradeLibraryBackends(context.Background())
			if on {
				Expect(lib.installed).To(Equal([]string{"official-backends/whisper@1.2.0"}), "switch on: want one upgrade")
			} else {
				Expect(lib.installed).To(BeEmpty(), "switch off: nothing may install")
			}
		}
	})

	It("does nothing without a library", func() {
		uc := &UpgradeChecker{appConfig: &config.ApplicationConfig{AutoUpgradeBackends: true}}
		uc.SetLibraryBackends(func() libraryUpgrader { return nil })
		uc.upgradeLibraryBackends(context.Background())
	})
})
