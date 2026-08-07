package config_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("default galleries", func() {
	It("serves the model gallery from index.localai.io with GitHub as a mirror", func() {
		var galleries []config.Gallery
		Expect(json.Unmarshal([]byte(config.DefaultGalleriesJSON), &galleries)).To(Succeed())
		Expect(galleries).To(HaveLen(1))
		Expect(galleries[0].Name).To(Equal("localai"))
		Expect(galleries[0].URL).To(Equal("https://index.localai.io/models"))
		Expect(galleries[0].Mirrors).To(Equal([]string{"github:mudler/LocalAI/gallery/index.yaml@master"}))
	})

	It("serves the backend gallery from index.localai.io with GitHub as a mirror", func() {
		var galleries []config.Gallery
		Expect(json.Unmarshal([]byte(config.DefaultBackendGalleriesJSON), &galleries)).To(Succeed())
		Expect(galleries).To(HaveLen(1))
		Expect(galleries[0].Name).To(Equal("localai"))
		Expect(galleries[0].URL).To(Equal("https://index.localai.io/backends"))
		Expect(galleries[0].Mirrors).To(Equal([]string{"github:mudler/LocalAI/backend/index.yaml@master"}))
	})

	// The mirror is the whole reason this default is safe to ship: if
	// index.localai.io is unreachable, an install must still resolve its
	// gallery from GitHub exactly as it did before.
	It("always leaves a reachable fallback behind the primary", func() {
		for _, raw := range []string{config.DefaultGalleriesJSON, config.DefaultBackendGalleriesJSON} {
			var galleries []config.Gallery
			Expect(json.Unmarshal([]byte(raw), &galleries)).To(Succeed())
			for _, g := range galleries {
				Expect(g.Mirrors).ToNot(BeEmpty(), "default %q has no mirror", g.Name)
			}
		}
	})

	// mustGalleries panics on malformed JSON, and these constants are also
	// fed to kong as defaults, so a typo would be a startup crash rather
	// than a config error.
	It("parses through the same path startup uses", func() {
		baseline := config.DefaultRuntimeBaseline()
		Expect(baseline.Galleries).To(HaveLen(1))
		Expect(baseline.BackendGalleries).To(HaveLen(1))
		Expect(baseline.Galleries[0].URL).To(Equal("https://index.localai.io/models"))
		Expect(baseline.BackendGalleries[0].URL).To(Equal("https://index.localai.io/backends"))
	})

	// The baseline decides "kong default" vs "user-configured", and since
	// the defaults grew a Mirrors slice that comparison runs through
	// GalleriesEqual rather than ==. A default install must still let the
	// persisted file apply, and any override - including one that differs
	// from the default only by its mirrors - must still be seen as env-set.
	Context("as the option-less startup baseline", func() {
		It("treats an untouched default list as not env-set, so the file applies", func() {
			o := config.DefaultRuntimeBaseline()
			saved := []config.Gallery{{Name: "mine", URL: "https://example.com/index.yaml"}}
			savedBackends := []config.Gallery{{Name: "mine-backends", URL: "https://example.com/backends.yaml"}}
			o.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{
				Galleries:        &saved,
				BackendGalleries: &savedBackends,
			})
			Expect(o.Galleries).To(Equal(saved))
			Expect(o.BackendGalleries).To(Equal(savedBackends))
		})

		It("treats LOCALAI_GALLERIES as env-set even when it only drops the mirror", func() {
			o := config.DefaultRuntimeBaseline()
			// Same primary as the default, mirror removed: env-set.
			o.Galleries = []config.Gallery{{Name: "localai", URL: "https://index.localai.io/models"}}
			saved := []config.Gallery{{Name: "file", URL: "https://file.example/index.yaml"}}
			o.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{Galleries: &saved})
			Expect(o.Galleries[0].Name).To(Equal("localai"),
				"an env list differing only by mirrors must still win over the file")
		})
	})
})
