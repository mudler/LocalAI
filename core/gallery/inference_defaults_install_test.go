package gallery_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

// The recommended sampling parameters for a model family are applied at install
// and persisted into the model YAML. Persisting them is only worth anything if
// they are written where the loader reads them back: PredictionOptions is nested
// under "parameters" in ModelConfig, so a top level "temperature" key parses
// without error and is then ignored for the life of the model.
//
// The expected values are read from the family table rather than written out
// here, so that retuning a family stays a one file change.
//
// Nothing here reaches the network.
var _ = Describe("Inference defaults persisted at install", func() {
	var tempdir string
	var galleries []config.Gallery
	var systemState *system.SystemState
	// The gallery listing is cached on the name and URL pair, so every spec
	// needs a gallery of its own or it reads the previous spec's catalog.
	galleryRevision := 0

	// The name has to contain a pattern from inference_defaults.json, otherwise
	// no defaults are applied and every assertion below passes vacuously.
	const modelName = "qwen3.5-install-defaults"

	newGallery := func(entries ...gallery.GalleryModel) {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		name := fmt.Sprintf("inference-defaults-%d", galleryRevision)
		galleryRevision++
		galleryPath := filepath.Join(tempdir, name+".yaml")
		Expect(os.WriteFile(galleryPath, out, 0600)).To(Succeed())
		galleries = []config.Gallery{{Name: name, URL: "file://" + galleryPath}}
	}

	install := func(name string) error {
		return gallery.InstallModelFromGallery(
			context.TODO(), galleries, []config.Gallery{}, systemState, nil,
			name, gallery.GalleryModel{}, func(string, string, string, float64) {}, false, false, false)
	}

	installedConfig := func(name string) map[string]any {
		dat, err := os.ReadFile(filepath.Join(tempdir, name+".yaml"))
		Expect(err).ToNot(HaveOccurred())
		content := map[string]any{}
		Expect(yaml.Unmarshal(dat, &content)).To(Succeed())
		return content
	}

	// Seeding the weights keeps the install off the network: the downloader
	// treats an already-present destination with no declared sha256 as fetched.
	// extra goes into parameters:, so a spec can pin a value the defaults would
	// otherwise supply.
	seedGallery := func(extra map[string]any) {
		Expect(os.WriteFile(filepath.Join(tempdir, "weights.gguf"), []byte("weights"), 0600)).To(Succeed())

		params := map[string]any{"model": "weights.gguf"}
		maps.Copy(params, extra)

		e := gallery.GalleryModel{Overrides: map[string]any{
			"backend":    "llama-cpp",
			"parameters": params,
		}}
		e.Name = modelName
		e.AdditionalFiles = []gallery.File{{Filename: "weights.gguf", URI: "https://example.com/weights.gguf"}}
		newGallery(e)
	}

	// Guards the fixture itself. If the name stops matching a family the specs
	// below would still pass while asserting nothing at all.
	expectedFamily := func() map[string]float64 {
		family := config.MatchModelFamily(modelName)
		Expect(family).ToNot(BeEmpty(), "fixture name no longer matches a family in inference_defaults.json")
		return family
	}

	BeforeEach(func() {
		var err error
		tempdir, err = os.MkdirTemp("", "inference-defaults-install")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempdir)).To(Succeed()) })

		systemState, err = system.GetSystemState(system.WithModelPath(tempdir))
		Expect(err).ToNot(HaveOccurred())
	})

	It("writes them under parameters, where the loader reads them back", func() {
		family := expectedFamily()
		seedGallery(nil)

		Expect(install(modelName)).To(Succeed())

		params, ok := installedConfig(modelName)["parameters"].(map[string]any)
		Expect(ok).To(BeTrue(), "parameters should be a map")

		for key, want := range family {
			Expect(params).To(HaveKey(key))
			Expect(params[key]).To(BeNumerically("==", want), "parameters.%s", key)
		}
	})

	It("does not leave them at the top level, where they are ignored", func() {
		family := expectedFamily()
		seedGallery(nil)

		Expect(install(modelName)).To(Succeed())

		cfg := installedConfig(modelName)
		for key := range family {
			Expect(cfg).ToNot(HaveKey(key), "%s at the top level is never read", key)
		}
	})

	It("leaves a value the entry already sets alone", func() {
		family := expectedFamily()
		Expect(family).To(HaveKey("temperature"))
		Expect(family["temperature"]).ToNot(BeNumerically("==", 0.05), "pick a value the family does not use")

		seedGallery(map[string]any{"temperature": 0.05})

		Expect(install(modelName)).To(Succeed())

		params, ok := installedConfig(modelName)["parameters"].(map[string]any)
		Expect(ok).To(BeTrue(), "parameters should be a map")
		Expect(params["temperature"]).To(BeNumerically("==", 0.05))
	})

	// An entry that binds a primary artifact carries no files: of its own, so it
	// takes the other branch of the install and none of the specs above reach it.
	// It is also the one branch that already re-marshalled, which is why the
	// defaults did land on disk there, at the top level where nothing reads them.
	It("writes them under parameters on the artifact binding path too", func() {
		family := expectedFamily()

		definition := &gallery.ModelConfig{ConfigFile: `
backend: transformers
artifacts:
  - name: model
    target: model
    source:
      type: huggingface
      repo: owner/repo
parameters:
  model: owner/repo
`}
		// Standing in for the materializer keeps the install off the network.
		materializer := &fakeArtifactMaterializer{result: modelartifacts.Result{
			Spec: modelartifacts.Spec{
				Name: "model", Target: "model",
				Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
				Resolved: &modelartifacts.Resolved{
					Endpoint: "https://huggingface.co",
					Revision: "0123456789abcdef0123456789abcdef01234567",
					CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				},
			},
			RelativePath: ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot",
		}}

		_, err := gallery.InstallModel(context.TODO(), systemState, modelName, definition, nil, nil, false,
			gallery.WithArtifactMaterializer(materializer))
		Expect(err).ToNot(HaveOccurred())

		cfg := installedConfig(modelName)
		params, ok := cfg["parameters"].(map[string]any)
		Expect(ok).To(BeTrue(), "parameters should be a map")
		for key, want := range family {
			Expect(params).To(HaveKey(key))
			Expect(params[key]).To(BeNumerically("==", want), "parameters.%s", key)
			Expect(cfg).ToNot(HaveKey(key), "%s at the top level is never read", key)
		}
	})
})
