package gallery_test

import (
	"context"
	"os"
	"path/filepath"

	"dario.cat/mergo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("GalleryModel meta entries", func() {
	It("is not meta when it has no candidates", func() {
		m := gallery.GalleryModel{}
		m.Name = "plain"
		Expect(m.IsMeta()).To(BeFalse())
	})

	It("is meta when it has candidates", func() {
		m := gallery.GalleryModel{Candidates: []gallery.Candidate{{Model: "x"}}}
		Expect(m.IsMeta()).To(BeTrue())
	})

	It("parses a candidate list from gallery YAML in order", func() {
		var m gallery.GalleryModel
		err := yaml.Unmarshal([]byte(`
name: qwen3-8b
url: "github:example/repo/qwen3-8b-gguf-q4.yaml@master"
candidates:
  - model: qwen3-8b-vllm-awq
    capability: nvidia
    min_vram: 20GiB
  - model: qwen3-8b-gguf-q4
`), &m)
		Expect(err).ToNot(HaveOccurred())
		Expect(m.IsMeta()).To(BeTrue())
		Expect(m.Candidates).To(HaveLen(2))
		Expect(m.Candidates[0].Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(m.Candidates[0].Capability).To(Equal("nvidia"))
		Expect(m.Candidates[0].MinVRAM).To(Equal("20GiB"))
		Expect(m.Candidates[1].Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(m.Candidates[1].Capability).To(BeEmpty())
	})
})

var _ = Describe("ResolveMetaModel", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	newModel := func(name, url, description, icon string) *gallery.GalleryModel {
		m := &gallery.GalleryModel{}
		m.Name = name
		m.URL = url
		m.Description = description
		m.Icon = icon
		return m
	}

	var models []*gallery.GalleryModel
	var meta *gallery.GalleryModel

	BeforeEach(func() {
		concreteVLLM := newModel("qwen3-8b-vllm-awq", "file://vllm.yaml", "AWQ variant", "vllm.png")
		concreteGGUF := newModel("qwen3-8b-gguf-q4", "file://gguf.yaml", "GGUF variant", "gguf.png")
		meta = newModel("qwen3-8b", "file://gguf.yaml", "Qwen3 8B", "qwen.png")
		meta.Tags = []string{"llm"}
		meta.Candidates = []gallery.Candidate{
			{Model: "qwen3-8b-vllm-awq", Capability: "nvidia", MinVRAM: "20GiB"},
			{Model: "qwen3-8b-gguf-q4"},
		}
		models = []*gallery.GalleryModel{concreteVLLM, concreteGGUF, meta}
	})

	It("installs the concrete payload under the meta's name", func() {
		resolved, candidate, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(resolved.Name).To(Equal("qwen3-8b"))
		Expect(resolved.URL).To(Equal("file://vllm.yaml"))
	})

	It("presents the meta's metadata, not the variant's", func() {
		resolved, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.Description).To(Equal("Qwen3 8B"))
		Expect(resolved.Icon).To(Equal("qwen.png"))
		Expect(resolved.Tags).To(ConsistOf("llm"))
	})

	It("keeps the name stable when a variant is pinned", func() {
		resolved, candidate, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "qwen3-8b-gguf-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.Name).To(Equal("qwen3-8b"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("detaches the resolved entry from the gallery's own maps and slices", func() {
		models[0].Overrides = map[string]any{"f16": true}
		models[0].AdditionalFiles = []gallery.File{{Filename: "a.bin"}}

		resolved, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())

		// The install path merges the caller's request overrides into this map in
		// place, so aliasing the gallery's map would write one caller's request
		// into the shared catalog and leak it into every later install.
		resolved.Overrides["f16"] = false
		resolved.Overrides["threads"] = 4
		Expect(models[0].Overrides).To(HaveKeyWithValue("f16", true))
		Expect(models[0].Overrides).ToNot(HaveKey("threads"))

		resolved.AdditionalFiles[0].Filename = "mutated.bin"
		Expect(models[0].AdditionalFiles[0].Filename).To(Equal("a.bin"))

		resolved.Tags = append(resolved.Tags, "extra")
		Expect(meta.Tags).To(ConsistOf("llm"))
	})

	It("detaches nested override maps from the gallery's own entry", func() {
		models[0].Overrides = map[string]any{
			"parameters": map[string]any{"model": "real-variant.gguf"},
			"stopwords":  []any{"</s>"},
		}

		resolved, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())

		// Cloning only the top level would leave this inner map shared with the
		// gallery entry, so writing through the resolved copy would rewrite the
		// catalog's own payload.
		resolved.Overrides["parameters"].(map[string]any)["model"] = "callers-choice.gguf"
		resolved.Overrides["stopwords"].([]any)[0] = "<|im_end|>"

		Expect(models[0].Overrides["parameters"]).To(HaveKeyWithValue("model", "real-variant.gguf"))
		Expect(models[0].Overrides["stopwords"]).To(Equal([]any{"</s>"}))
	})

	It("does not write the caller's overrides back into the gallery entry", func() {
		models[0].Overrides = map[string]any{"parameters": map[string]any{"model": "real-variant.gguf"}}

		resolved, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())

		// This is exactly what the install path does with the caller's request
		// overrides, and mergo recurses into nested maps and overwrites them in
		// place. Asserting against the in-memory catalog is the only way to
		// observe the leak: re-reading the gallery from disk re-unmarshals fresh
		// maps and would pass whether or not the resolved entry was detached.
		requestOverrides := map[string]any{"parameters": map[string]any{"model": "callers-choice.gguf"}}
		Expect(mergo.Merge(&resolved.Overrides, requestOverrides, mergo.WithOverride)).To(Succeed())

		Expect(resolved.Overrides["parameters"]).To(HaveKeyWithValue("model", "callers-choice.gguf"))
		Expect(models[0].Overrides["parameters"]).To(HaveKeyWithValue("model", "real-variant.gguf"))
	})

	It("errors when a candidate references a missing entry", func() {
		meta.Candidates = []gallery.Candidate{{Model: "does-not-exist"}}
		_, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does-not-exist"))
	})

	It("refuses a candidate that is itself a meta entry", func() {
		nested := newModel("nested", "", "", "")
		nested.Candidates = []gallery.Candidate{{Model: "qwen3-8b-gguf-q4"}}
		models = append(models, nested)
		meta.Candidates = []gallery.Candidate{{Model: "nested"}}
		_, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nested"))
	})

	It("surfaces a bad pin", func() {
		_, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "nope")
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
	})
})

var _ = Describe("InstallModelFromGallery with meta entries", func() {
	var tempdir string
	var galleries []config.Gallery
	var systemState *system.SystemState

	// The variants are described with an inline config_file rather than a URL so
	// the whole install runs off the local filesystem with no network access.
	// The meta entry keeps a url as well, because a real meta entry carries one
	// as a fallback for older LocalAI releases that do not understand candidates,
	// and installing that fallback instead of a variant is exactly the regression
	// these specs guard against.
	newGallery := func(meta gallery.GalleryModel, variants ...gallery.GalleryModel) {
		fallback := gallery.ModelConfig{
			Name:        "legacy-fallback",
			Description: "legacy fallback payload",
			ConfigFile:  "backend: fallback-backend\n",
		}
		fallbackYAML, err := yaml.Marshal(fallback)
		Expect(err).ToNot(HaveOccurred())
		fallbackPath := filepath.Join(tempdir, "fallback.yaml")
		Expect(os.WriteFile(fallbackPath, fallbackYAML, 0600)).To(Succeed())

		meta.URL = "file://" + fallbackPath
		entries := append([]gallery.GalleryModel{meta}, variants...)

		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		galleryPath := filepath.Join(tempdir, "gallery.yaml")
		Expect(os.WriteFile(galleryPath, out, 0600)).To(Succeed())

		galleries = []config.Gallery{{Name: "test", URL: "file://" + galleryPath}}
	}

	variant := func(name, backend string) gallery.GalleryModel {
		m := gallery.GalleryModel{ConfigFile: map[string]any{"backend": backend}}
		m.Name = name
		m.Description = "variant " + name
		return m
	}

	// urlVariant describes a variant through a url rather than an inline
	// config_file. The distinction matters for the recorded name: the url branch
	// reads a name out of the fetched config, and that name is the variant's own,
	// so only the meta-name overlay can keep the record under the meta's name.
	// The inline config_file branch seeds the name from the already-renamed
	// resolved entry and so cannot observe the overlay at all.
	urlVariant := func(name, backend string) gallery.GalleryModel {
		payload := gallery.ModelConfig{
			Name:        name,
			Description: "variant " + name,
			ConfigFile:  "backend: " + backend + "\n",
		}
		out, err := yaml.Marshal(payload)
		Expect(err).ToNot(HaveOccurred())
		payloadPath := filepath.Join(tempdir, "payload-"+name+".yaml")
		Expect(os.WriteFile(payloadPath, out, 0600)).To(Succeed())

		m := gallery.GalleryModel{}
		m.Name = name
		m.Description = "variant " + name
		m.URL = "file://" + payloadPath
		return m
	}

	metaEntry := func(name string, candidates ...string) gallery.GalleryModel {
		m := gallery.GalleryModel{}
		m.Name = name
		m.Description = "the meta entry"
		m.Icon = "meta.png"
		for _, c := range candidates {
			m.Candidates = append(m.Candidates, gallery.Candidate{Model: c})
		}
		return m
	}

	install := func(name string, req gallery.GalleryModel, options ...gallery.InstallOption) error {
		return gallery.InstallModelFromGallery(
			context.TODO(), galleries, []config.Gallery{}, systemState, nil,
			name, req, func(string, string, string, float64) {}, false, false, false, options...)
	}

	installedBackend := func(name string) string {
		dat, err := os.ReadFile(filepath.Join(tempdir, name+".yaml"))
		Expect(err).ToNot(HaveOccurred())
		content := map[string]any{}
		Expect(yaml.Unmarshal(dat, &content)).To(Succeed())
		return content["backend"].(string)
	}

	BeforeEach(func() {
		var err error
		tempdir, err = os.MkdirTemp("", "meta-install")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempdir)).To(Succeed()) })

		systemState, err = system.GetSystemState(system.WithModelPath(tempdir))
		Expect(err).ToNot(HaveOccurred())

		newGallery(
			metaEntry("qwen3-8b", "qwen3-8b-variant-a", "qwen3-8b-variant-b"),
			variant("qwen3-8b-variant-a", "variant-a-backend"),
			variant("qwen3-8b-variant-b", "variant-b-backend"),
		)
	})

	It("installs the resolved variant's payload, not the meta's url fallback", func() {
		Expect(install("qwen3-8b", gallery.GalleryModel{})).To(Succeed())

		// If meta-ness stopped winning over the url, this would be
		// "fallback-backend" and every meta entry would silently install the
		// legacy payload instead of a hardware-appropriate variant.
		Expect(installedBackend("qwen3-8b")).To(Equal("variant-a-backend"))
	})

	It("round-trips the resolution record to disk under the meta's name", func() {
		// The variant is described by url so its payload carries its own name.
		// Without the meta-name overlay the record persists as
		// "qwen3-8b-variant-a", and the stable name a meta entry exists to
		// provide is lost the moment anything reads the record back.
		newGallery(
			metaEntry("qwen3-8b", "qwen3-8b-variant-a", "qwen3-8b-variant-b"),
			urlVariant("qwen3-8b-variant-a", "variant-a-backend"),
			urlVariant("qwen3-8b-variant-b", "variant-b-backend"),
		)

		Expect(install("qwen3-8b", gallery.GalleryModel{})).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.MetaName).To(Equal("qwen3-8b"))
		Expect(record.ResolvedVariant).To(Equal("qwen3-8b-variant-a"))
		Expect(record.PinnedVariant).To(BeEmpty())
		Expect(record.Name).To(Equal("qwen3-8b"))
		Expect(record.Description).To(Equal("the meta entry"))
		Expect(installedBackend("qwen3-8b")).To(Equal("variant-a-backend"))
	})

	It("records a pin and honors it on a plain reinstall", func() {
		Expect(install("qwen3-8b", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-variant-b"))).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-variant-b"))
		Expect(record.ResolvedVariant).To(Equal("qwen3-8b-variant-b"))
		Expect(installedBackend("qwen3-8b")).To(Equal("variant-b-backend"))

		// No WithVariant this time: hardware resolution would pick variant-a, so
		// only the recalled pin can keep this on variant-b.
		Expect(install("qwen3-8b", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b")).To(Equal("variant-b-backend"))

		record, err = gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-variant-b"))
	})

	It("honors a pin recorded under a custom install name", func() {
		req := gallery.GalleryModel{}
		req.Name = "prod-llm"

		Expect(install("qwen3-8b", req, gallery.WithVariant("qwen3-8b-variant-b"))).To(Succeed())

		// The pin is written under the installed name, so the recall must read it
		// back under that name too and not under the gallery entry's name.
		record, err := gallery.GetLocalModelConfiguration(tempdir, "prod-llm")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-variant-b"))
		Expect(record.MetaName).To(Equal("qwen3-8b"))
		Expect(installedBackend("prod-llm")).To(Equal("variant-b-backend"))

		Expect(install("qwen3-8b", req)).To(Succeed())
		Expect(installedBackend("prod-llm")).To(Equal("variant-b-backend"))
	})

	It("writes each declared url only once into the persisted gallery file", func() {
		meta := metaEntry("qwen3-8b", "qwen3-8b-variant-a")
		meta.URLs = []string{"https://example.invalid/qwen3"}
		newGallery(meta, variant("qwen3-8b-variant-a", "variant-a-backend"))

		Expect(install("qwen3-8b", gallery.GalleryModel{})).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.URLs).To(ConsistOf("https://example.invalid/qwen3"))
	})
})

var _ = Describe("legacy client compatibility", func() {
	It("exposes a url on every meta entry to clients that ignore candidates", func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
		Expect(err).ToNot(HaveOccurred())

		// Parse exactly as an older LocalAI release would: non-strictly, with
		// no knowledge of the candidates key.
		var legacy []struct {
			Name string `yaml:"name"`
			URL  string `yaml:"url"`
		}
		Expect(yaml.Unmarshal(data, &legacy)).To(Succeed())

		var current []gallery.GalleryModel
		Expect(yaml.Unmarshal(data, &current)).To(Succeed())

		urlByName := map[string]string{}
		for _, e := range legacy {
			urlByName[e.Name] = e.URL
		}

		metaCount := 0
		for _, e := range current {
			if !e.IsMeta() {
				continue
			}
			metaCount++
			// Without a url an old client lists the entry and installs an
			// empty model, because it silently drops candidates.
			Expect(urlByName[e.Name]).ToNot(BeEmpty(),
				"meta entry %q is invisible payload-wise to older clients", e.Name)
		}
		Expect(metaCount).To(BeNumerically(">", 0),
			"expected at least one meta entry in the gallery index")
	})
})
