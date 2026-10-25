package gallery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// noProgress is the download callback the install specs do not care about.
func noProgress(string, string, string, float64) {}

// galleryModelNames lists what a listing produced, so a spec can say which
// entries survived without depending on their order.
func galleryModelNames(models GalleryElements[*GalleryModel]) []string {
	names := []string{}
	for _, m := range models {
		names = append(names, m.Name)
	}
	return names
}

var _ = Describe("gallery entry URLs relative to the gallery root", func() {
	BeforeEach(resetGalleryFailures)

	It("installs an entry whose url is relative to an oci:// gallery root", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/relative", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: "- name: relative-entry\n  url: base/virtual.yaml\n"},
			{title: "base/virtual.yaml", body: "name: virtual\nconfig_file: |\n  backend: llama\n"},
		})

		base := tempModelsDir()
		systemState, err := system.GetSystemState(system.WithModelPath(base))
		Expect(err).ToNot(HaveOccurred())
		galleries := []config.Gallery{{Name: "relative", URL: url}}

		models, err := AvailableGalleryModels(galleries, systemState)
		Expect(err).ToNot(HaveOccurred())
		Expect(galleryModelNames(models)).To(ConsistOf("relative-entry"))

		// The entry is only really resolved if it installs: the base config
		// has to be read back out of the unpacked artifact.
		err = InstallModelFromGallery(context.Background(), galleries, nil, systemState, nil,
			"relative@relative-entry", GalleryModel{}, noProgress, false, false, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(filepath.Join(base, "relative-entry.yaml")).To(BeAnExistingFile())
	})

	It("resolves a relative entry url against the directory of an http gallery index", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/gallery/index.yaml" {
				_, _ = w.Write([]byte("- name: relative-entry\n  url: base/virtual.yaml\n" +
					"- name: absolute-entry\n  url: https://example.invalid/some/other.yaml\n"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		DeferCleanup(srv.Close)

		base := tempModelsDir()
		systemState, err := system.GetSystemState(system.WithModelPath(base))
		Expect(err).ToNot(HaveOccurred())

		models, err := AvailableGalleryModels([]config.Gallery{{
			Name: "http-relative",
			URL:  srv.URL + "/gallery/index.yaml",
		}}, systemState)
		Expect(err).ToNot(HaveOccurred())
		Expect(galleryModelNames(models)).To(ConsistOf("relative-entry", "absolute-entry"))

		byName := map[string]string{}
		for _, m := range models {
			byName[m.Name] = m.URL
		}
		Expect(byName["relative-entry"]).To(Equal(srv.URL + "/gallery/base/virtual.yaml"))
		// An entry that names its own source keeps it, whatever the gallery
		// root is.
		Expect(byName["absolute-entry"]).To(Equal("https://example.invalid/some/other.yaml"))
	})

	It("refuses an entry url that climbs out of the gallery root", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/escaping", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: "- name: escaping-entry\n  url: ../../../etc/passwd\n" +
				"- name: honest-entry\n  url: base/virtual.yaml\n"},
			{title: "base/virtual.yaml", body: "name: virtual\nconfig_file: |\n  backend: llama\n"},
		})

		base := tempModelsDir()
		systemState, err := system.GetSystemState(system.WithModelPath(base))
		Expect(err).ToNot(HaveOccurred())
		galleries := []config.Gallery{{Name: "escaping", URL: url}}

		models, err := AvailableGalleryModels(galleries, systemState)
		Expect(err).ToNot(HaveOccurred())
		// The escaping entry is dropped, and a single bad entry does not cost
		// the user the rest of the gallery.
		Expect(galleryModelNames(models)).To(ConsistOf("honest-entry"))

		err = InstallModelFromGallery(context.Background(), galleries, nil, systemState, nil,
			"escaping@escaping-entry", GalleryModel{}, noProgress, false, false, false)
		Expect(err).To(HaveOccurred())
	})
})
