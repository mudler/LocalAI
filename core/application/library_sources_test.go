package application

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/library"
)

// shipped sets the galleries a run with the default flags starts with.
func shipped(cfg *config.ApplicationConfig) {
	Expect(json.Unmarshal([]byte(config.DefaultGalleriesJSON), &cfg.Galleries)).To(Succeed())
	Expect(json.Unmarshal([]byte(config.DefaultBackendGalleriesJSON), &cfg.BackendGalleries)).To(Succeed())
}

var _ = Describe("the official libraries switch", func() {
	It("applyLibraries follows the official switch in both states", func() {
		dir := GinkgoT().TempDir()
		cfg := config.NewApplicationConfig(config.WithDataPath(dir))
		shipped(cfg)
		library.Option(dir, true, false, true)(cfg)
		wantModels, wantBackends := len(cfg.Galleries), len(cfg.BackendGalleries)
		Expect(wantModels).ToNot(BeZero(), "the shipped galleries were not mapped: %d, %d", wantModels, wantBackends)
		Expect(wantBackends).ToNot(BeZero(), "the shipped galleries were not mapped: %d, %d", wantModels, wantBackends)
		// A gallery of the user, so the off state has something left to read.
		store, err := library.Open(dir, true, false)
		Expect(err).ToNot(HaveOccurred())
		_, err = library.Add(store, "model", "https://example.org/team.yaml", "team", "Team", "gallery")
		Expect(err).ToNot(HaveOccurred())
		a := &Application{applicationConfig: cfg}

		cfg.OfficialLibraries = false
		Expect(a.ApplyLibraries()).To(Succeed())
		Expect(cfg.Galleries).To(HaveLen(1), "off: models %+v backends %+v", cfg.Galleries, cfg.BackendGalleries)
		Expect(cfg.Galleries[0].URL).To(Equal("https://example.org/team.yaml"), "off: models %+v", cfg.Galleries)
		Expect(cfg.BackendGalleries).To(BeEmpty(), "off: backends %+v", cfg.BackendGalleries)

		cfg.OfficialLibraries = true
		Expect(a.ApplyLibraries()).To(Succeed())
		Expect(cfg.Galleries).To(HaveLen(wantModels+1), "on: models %+v", cfg.Galleries)
		Expect(cfg.BackendGalleries).To(HaveLen(wantBackends), "on: backends %+v", cfg.BackendGalleries)
	})

	It("drops the default galleries at startup when the official libraries are off", func() {
		dir := GinkgoT().TempDir()
		cfg := config.NewApplicationConfig(config.WithDataPath(dir))
		shipped(cfg)
		library.Option(dir, true, false, true)(cfg)

		cfg.OfficialLibraries = true
		applyOfficialLibrariesAtStartup(cfg)
		Expect(cfg.Galleries).ToNot(BeEmpty(), "on: the default galleries were dropped")
		Expect(cfg.BackendGalleries).ToNot(BeEmpty(), "on: the default galleries were dropped")
		cfg.OfficialLibraries = false
		applyOfficialLibrariesAtStartup(cfg)
		Expect(cfg.Galleries).To(BeEmpty(), "off: models %+v", cfg.Galleries)
		Expect(cfg.BackendGalleries).To(BeEmpty(), "off: backends %+v", cfg.BackendGalleries)
	})
})
