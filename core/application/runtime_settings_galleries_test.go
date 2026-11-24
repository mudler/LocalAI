package application

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A manual edit of runtime_settings.json reaches the live config through the
// file watcher, not the settings endpoint, and must drop the cached model
// listing the same way: the UI lists from that cache.
var _ = Describe("file watcher: galleries", func() {
	It("drops the cached model listing when the galleries change", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("- name: acme-model\n"))
		}))
		DeferCleanup(srv.Close)
		gallery.ResetGalleryModelCache()
		DeferCleanup(gallery.ResetGalleryModelCache)

		st, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		live := config.NewApplicationConfig()
		live.SystemState = st
		// At the default list, so the file value is not taken as env-set.
		live.Galleries = config.DefaultRuntimeBaseline().Galleries

		policy := config.GalleryVerification{Issuer: "https://token.actions.githubusercontent.com", IdentityRegex: "^https://github.com/acme/.*$"}
		first, err := gallery.AvailableGalleryModelsCached([]config.Gallery{{Name: "old", URL: srv.URL}}, st)
		Expect(err).ToNot(HaveOccurred())
		Expect(first).To(HaveLen(1))

		body, err := json.Marshal(map[string]any{
			"galleries": []config.Gallery{{Name: "acme", URL: srv.URL, Verification: &policy}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(readRuntimeSettingsJson(config.ApplicationConfig{})(body, live)).To(Succeed())
		Expect(live.Galleries).To(HaveLen(1), "precondition: the file value was applied")

		models, err := gallery.AvailableGalleryModelsCached(live.Galleries, st)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1))
		Expect(models[0].Gallery.Name).To(Equal("acme"), "the listing cached under the old gallery configuration was served")
	})
})
