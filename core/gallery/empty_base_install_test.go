package gallery_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// An entry declaring neither url: nor config_file: is installed on an empty
// base config, with overrides: and files: supplying everything. These specs
// cover that path, the payload rule that still rejects an entry with nothing to
// install, and the two older paths, which must be untouched.
//
// Nothing here reaches the network. The whole point of the empty-base path is
// that it fetches nothing, and a spec that quietly went to GitHub would be
// asserting the opposite of the change.
var _ = Describe("InstallModelFromGallery with an empty base config", func() {
	var tempdir string
	var galleries []config.Gallery
	var systemState *system.SystemState
	// The gallery listing is cached on the name and URL pair, so every spec
	// needs a gallery of its own or it reads the previous spec's catalog.
	galleryRevision := 0

	newGallery := func(entries ...gallery.GalleryModel) {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		name := fmt.Sprintf("empty-base-%d", galleryRevision)
		galleryRevision++
		galleryPath := filepath.Join(tempdir, name+".yaml")
		Expect(os.WriteFile(galleryPath, out, 0600)).To(Succeed())
		galleries = []config.Gallery{{Name: name, URL: "file://" + galleryPath}}
	}

	install := func(name string, req gallery.GalleryModel, options ...gallery.InstallOption) error {
		return gallery.InstallModelFromGallery(
			context.TODO(), galleries, []config.Gallery{}, systemState, nil,
			name, req, func(string, string, string, float64) {}, false, false, false, options...)
	}

	installedConfig := func(name string) map[string]any {
		dat, err := os.ReadFile(filepath.Join(tempdir, name+".yaml"))
		Expect(err).ToNot(HaveOccurred())
		content := map[string]any{}
		Expect(yaml.Unmarshal(dat, &content)).To(Succeed())
		return content
	}

	// localWeights lets a spec carry a files: list without leaving the
	// filesystem. The downloader treats an already-present destination with no
	// declared sha256 as fetched and skips it, so seeding the file is what keeps
	// these specs off the network. The URI is still authored, because the
	// installer walks the list either way and a missing one would not exercise
	// the same code.
	localWeights := func(name string) gallery.File {
		Expect(os.WriteFile(filepath.Join(tempdir, name), []byte("weights for "+name), 0600)).To(Succeed())
		return gallery.File{Filename: name, URI: "https://example.com/" + name}
	}

	BeforeEach(func() {
		var err error
		tempdir, err = os.MkdirTemp("", "empty-base-install")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempdir)).To(Succeed()) })

		systemState, err = system.GetSystemState(system.WithModelPath(tempdir))
		Expect(err).ToNot(HaveOccurred())
	})

	It("installs an entry that declares only overrides and files", func() {
		e := gallery.GalleryModel{Overrides: map[string]any{
			"backend":    "ds4",
			"parameters": map[string]any{"model": "weights.gguf"},
		}}
		e.Name = "overrides-only"
		e.Description = "an entry with no base config"
		e.AdditionalFiles = []gallery.File{localWeights("weights.gguf")}
		newGallery(e)

		Expect(install("overrides-only", gallery.GalleryModel{})).To(Succeed())

		cfg := installedConfig("overrides-only")
		Expect(cfg["backend"]).To(Equal("ds4"))
		Expect(cfg["parameters"]).To(HaveKeyWithValue("model", "weights.gguf"))
		// The name comes from the install, never from a base config, which is
		// what the several hundred virtual.yaml entries were getting wrong: they
		// inherited the stub's name.
		Expect(cfg["name"]).To(Equal("overrides-only"))
		Expect(filepath.Join(tempdir, "weights.gguf")).To(BeAnExistingFile())
	})

	It("installs an entry that declares only files", func() {
		e := gallery.GalleryModel{}
		e.Name = "files-only"
		e.AdditionalFiles = []gallery.File{localWeights("plain.gguf")}
		newGallery(e)

		Expect(install("files-only", gallery.GalleryModel{})).To(Succeed())
		Expect(filepath.Join(tempdir, "plain.gguf")).To(BeAnExistingFile())
	})

	// Half the point of the change is that the empty-base path costs no fetch,
	// and an assertion that nothing was fetched is worthless unless something
	// could have been. So the control runs the same install with a url: pointing
	// at a base config that is not there: it must fail, proving a url IS read on
	// this path, and only then does the same fixture without the url passing
	// mean the read was skipped rather than silently succeeding.
	Describe("the base config fetch", func() {
		var missing string

		BeforeEach(func() {
			missing = "file://" + filepath.Join(tempdir, "no-such-base.yaml")
			Expect(filepath.Join(tempdir, "no-such-base.yaml")).ToNot(BeAnExistingFile())
		})

		It("is attempted when the entry declares a url", func() {
			e := gallery.GalleryModel{Overrides: map[string]any{"backend": "ds4"}}
			e.Name = "with-url"
			e.URL = missing
			newGallery(e)

			// If this ever starts passing, the control has stopped controlling
			// and the spec below proves nothing.
			Expect(install("with-url", gallery.GalleryModel{})).To(HaveOccurred())
		})

		It("is skipped entirely when the entry declares none", func() {
			e := gallery.GalleryModel{Overrides: map[string]any{"backend": "ds4"}}
			e.Name = "without-url"
			newGallery(e)

			Expect(install("without-url", gallery.GalleryModel{})).To(Succeed())
			Expect(installedConfig("without-url")["backend"]).To(Equal("ds4"))
		})
	})

	Describe("an entry with nothing to install", func() {
		// No url, no config_file, no overrides and no files. Accepting this
		// would write an empty model directory and report success, which is the
		// authoring mistake the relaxation would otherwise hide.
		emptyEntry := func() gallery.GalleryModel {
			e := gallery.GalleryModel{}
			e.Name = "hollow"
			// urls: is the informational link list. It reads like a payload and
			// is not one.
			e.URLs = []string{"https://huggingface.co/example/hollow"}
			return e
		}

		It("is refused rather than installed empty", func() {
			newGallery(emptyEntry())

			err := install("hollow", gallery.GalleryModel{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("installs nothing"))
			Expect(err.Error()).To(ContainSubstring("hollow"))
			Expect(filepath.Join(tempdir, "hollow.yaml")).ToNot(BeAnExistingFile())
		})

		It("is accepted when the caller's request supplies the payload", func() {
			// The request's overrides are merged into the install exactly as the
			// entry's own are, so a caller who brings them really has asked for
			// something installable.
			newGallery(emptyEntry())

			req := gallery.GalleryModel{Overrides: map[string]any{"backend": "llama-cpp"}}
			Expect(install("hollow", req)).To(Succeed())
			Expect(installedConfig("hollow")["backend"]).To(Equal("llama-cpp"))
		})
	})

	// The two older paths are meant to be untouched by the relaxation.
	Describe("the pre-existing paths", func() {
		It("still installs an entry described by an inline config_file", func() {
			e := gallery.GalleryModel{ConfigFile: map[string]any{"backend": "llama-cpp"}}
			e.Name = "inline"
			newGallery(e)

			Expect(install("inline", gallery.GalleryModel{})).To(Succeed())
			Expect(installedConfig("inline")["backend"]).To(Equal("llama-cpp"))
		})

		It("still installs an entry described by a url", func() {
			payload, err := yaml.Marshal(gallery.ModelConfig{
				Name:       "fetched",
				ConfigFile: "backend: vllm\n",
			})
			Expect(err).ToNot(HaveOccurred())
			payloadPath := filepath.Join(tempdir, "payload.yaml")
			Expect(os.WriteFile(payloadPath, payload, 0600)).To(Succeed())

			e := gallery.GalleryModel{}
			e.Name = "from-url"
			e.URL = "file://" + payloadPath
			newGallery(e)

			Expect(install("from-url", gallery.GalleryModel{})).To(Succeed())
			Expect(installedConfig("from-url")["backend"]).To(Equal("vllm"))
		})
	})

	// One of the entries that shipped uninstallable, driven through the real
	// install path rather than only checked as text. Its files: are swapped for
	// a local one because the real ones are gigabytes on HuggingFace; its
	// overrides: are the catalog's own, so this proves the authored payload
	// lands.
	It("installs a previously broken index entry once the empty base is allowed", func() {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())

		var e gallery.GalleryModel
		for _, candidate := range entries {
			if candidate.Name == "liquidai_lfm2-1.2b-rag" {
				e = candidate
				break
			}
		}
		Expect(e.Name).To(Equal("liquidai_lfm2-1.2b-rag"))
		// The defect: no base config of any kind, which used to be fatal.
		Expect(e.URL).To(BeEmpty())
		Expect(e.ConfigFile).To(BeEmpty())
		Expect(e.Overrides).ToNot(BeEmpty())

		e.AdditionalFiles = []gallery.File{localWeights("LiquidAI_LFM2-1.2B-RAG-Q4_K_M.gguf")}
		newGallery(e)

		Expect(install(e.Name, gallery.GalleryModel{})).To(Succeed())
		cfg := installedConfig(e.Name)
		Expect(cfg["name"]).To(Equal(e.Name))
		// The catalog's own overrides, verbatim, laid over the empty base.
		Expect(cfg["parameters"]).To(Equal(e.Overrides["parameters"]))
		Expect(cfg["known_usecases"]).To(Equal(e.Overrides["known_usecases"]))
	})
})
