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

var _ = Describe("GalleryModel candidate declarations", func() {
	It("declares no candidates when the key is absent", func() {
		m := gallery.GalleryModel{}
		m.Name = "plain"
		Expect(m.HasCandidates()).To(BeFalse())
	})

	It("declares candidates when the key is present", func() {
		m := gallery.GalleryModel{Candidates: []gallery.Candidate{{Model: "x"}}}
		Expect(m.HasCandidates()).To(BeTrue())
	})

	It("parses an entry's own selection fields and its candidate list in order", func() {
		var m gallery.GalleryModel
		err := yaml.Unmarshal([]byte(`
name: qwen3-8b-gguf-q4
url: "github:example/repo/qwen3-8b-gguf-q4.yaml@master"
min_vram: 6GiB
capability: default
candidates:
  - model: qwen3-8b-vllm-awq
    capability: nvidia
    min_vram: 20GiB
  - model: qwen3-8b-gguf-q8
    min_vram: 10GiB
`), &m)
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Name).To(Equal("qwen3-8b-gguf-q4"))
		Expect(m.URL).To(Equal("github:example/repo/qwen3-8b-gguf-q4.yaml@master"))
		Expect(m.MinVRAM).To(Equal("6GiB"))
		Expect(m.Capability).To(Equal("default"))
		Expect(m.HasCandidates()).To(BeTrue())
		Expect(m.Candidates).To(HaveLen(2))
		Expect(m.Candidates[0].Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(m.Candidates[0].Capability).To(Equal("nvidia"))
		Expect(m.Candidates[0].MinVRAM).To(Equal("20GiB"))
		Expect(m.Candidates[1].Model).To(Equal("qwen3-8b-gguf-q8"))
		Expect(m.Candidates[1].Capability).To(BeEmpty())
	})
})

var _ = Describe("ResolveVariant", func() {
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
	var base *gallery.GalleryModel

	BeforeEach(func() {
		upgrade := newModel("qwen3-8b-vllm-awq", "file://vllm.yaml", "AWQ variant", "vllm.png")
		// The base is an ordinary, complete entry that happens to declare an
		// upgrade over itself, which is the whole point of the design.
		base = newModel("qwen3-8b-gguf-q4", "file://gguf.yaml", "Qwen3 8B Q4", "qwen.png")
		base.Tags = []string{"llm"}
		base.MinVRAM = "6GiB"
		base.Candidates = []gallery.Candidate{
			{Model: "qwen3-8b-vllm-awq", Capability: "nvidia", MinVRAM: "20GiB"},
		}
		models = []*gallery.GalleryModel{upgrade, base}
	})

	It("installs a matching candidate's payload under the entry's name", func() {
		resolved, candidate, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(resolved.Name).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://vllm.yaml"))
	})

	It("falls back to the entry's own payload when no candidate fits", func() {
		resolved, candidate, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("installs the entry even when the host misses the entry's own floor", func() {
		// There is nothing below the base, so refusing here would make an entry
		// that every older client installs fine uninstallable on new ones.
		resolved, candidate, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(1)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("strips the selection fields from the resolved entry", func() {
		// A resolved entry is a concrete install target. Leaving the fields on
		// it would let a second resolution pass fire on an already-resolved
		// entry.
		resolved, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.HasCandidates()).To(BeFalse())
		Expect(resolved.MinVRAM).To(BeEmpty())
		Expect(resolved.Capability).To(BeEmpty())
	})

	It("presents the entry's metadata, not the candidate's", func() {
		resolved, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.Description).To(Equal("Qwen3 8B Q4"))
		Expect(resolved.Icon).To(Equal("qwen.png"))
		Expect(resolved.Tags).To(ConsistOf("llm"))
	})

	It("honors a pin naming the entry itself", func() {
		// The entry is the last element of its own candidate list, so its own
		// name has to be a usable pin: it is how an operator declines an
		// upgrade their hardware would otherwise take.
		resolved, candidate, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "qwen3-8b-gguf-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.Name).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("honors a pin the hardware does not satisfy", func() {
		resolved, candidate, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(2)}, "qwen3-8b-vllm-awq")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(resolved.URL).To(Equal("file://vllm.yaml"))
	})

	It("detaches the resolved entry from the gallery's own maps and slices", func() {
		models[0].Overrides = map[string]any{"f16": true}
		models[0].AdditionalFiles = []gallery.File{{Filename: "a.bin"}}

		resolved, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
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
		Expect(base.Tags).To(ConsistOf("llm"))
	})

	It("detaches the resolved entry even when it resolves to the entry itself", func() {
		// Resolving to the base returns a copy of the very entry the gallery
		// holds, which is the case most likely to alias it.
		base.Overrides = map[string]any{"parameters": map[string]any{"model": "q4.gguf"}}

		resolved, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).ToNot(HaveOccurred())

		resolved.Overrides["parameters"].(map[string]any)["model"] = "callers-choice.gguf"
		Expect(base.Overrides["parameters"]).To(HaveKeyWithValue("model", "q4.gguf"))
	})

	It("detaches nested override maps from the gallery's own entry", func() {
		models[0].Overrides = map[string]any{
			"parameters": map[string]any{"model": "real-variant.gguf"},
			"stopwords":  []any{"</s>"},
		}

		resolved, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
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

		resolved, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
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
		base.Candidates = []gallery.Candidate{{Model: "does-not-exist"}}
		_, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does-not-exist"))
	})

	It("refuses a candidate that declares candidates of its own", func() {
		nested := newModel("nested", "file://nested.yaml", "", "")
		nested.Candidates = []gallery.Candidate{{Model: "qwen3-8b-vllm-awq"}}
		models = append(models, nested)
		base.Candidates = []gallery.Candidate{{Model: "nested"}}
		_, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nested"))
	})

	It("surfaces a bad pin", func() {
		_, _, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "nope")
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
	})
})

var _ = Describe("InstallModelFromGallery with candidate entries", func() {
	var tempdir string
	var galleries []config.Gallery
	var systemState *system.SystemState

	// Every entry is described with an inline config_file rather than a URL so
	// the whole install runs off the local filesystem with no network access.
	newGallery := func(entries ...gallery.GalleryModel) {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		galleryPath := filepath.Join(tempdir, "gallery.yaml")
		Expect(os.WriteFile(galleryPath, out, 0600)).To(Succeed())

		galleries = []config.Gallery{{Name: "test", URL: "file://" + galleryPath}}
	}

	entry := func(name, backend string) gallery.GalleryModel {
		m := gallery.GalleryModel{ConfigFile: map[string]any{"backend": backend}}
		m.Name = name
		m.Description = "entry " + name
		return m
	}

	// urlEntry describes an entry through a url rather than an inline
	// config_file. The distinction matters for the recorded name: the url branch
	// reads a name out of the fetched config, and that name is the referenced
	// entry's own, so only the entry-name overlay can keep the record under the
	// name the user asked for. The inline config_file branch seeds the name from
	// the already-renamed resolved entry and so cannot observe the overlay.
	urlEntry := func(name, backend string) gallery.GalleryModel {
		payload := gallery.ModelConfig{
			Name:        name,
			Description: "entry " + name,
			ConfigFile:  "backend: " + backend + "\n",
		}
		out, err := yaml.Marshal(payload)
		Expect(err).ToNot(HaveOccurred())
		payloadPath := filepath.Join(tempdir, "payload-"+name+".yaml")
		Expect(os.WriteFile(payloadPath, out, 0600)).To(Succeed())

		m := gallery.GalleryModel{}
		m.Name = name
		m.Description = "entry " + name
		m.URL = "file://" + payloadPath
		return m
	}

	// withCandidates attaches upgrades to an otherwise ordinary entry. The
	// floors are absolute rather than relative to the host: "0GiB" always
	// matches and "10000GiB" never does, so these specs assert on resolution
	// rather than on whatever VRAM the machine running them happens to have.
	withCandidates := func(m gallery.GalleryModel, candidates ...gallery.Candidate) gallery.GalleryModel {
		m.Candidates = candidates
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
		tempdir, err = os.MkdirTemp("", "candidate-install")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempdir)).To(Succeed()) })

		systemState, err = system.GetSystemState(system.WithModelPath(tempdir))
		Expect(err).ToNot(HaveOccurred())
	})

	It("installs the entry's own payload when no candidate fits the host", func() {
		newGallery(
			withCandidates(entry("qwen3-8b-q4", "base-backend"),
				gallery.Candidate{Model: "qwen3-8b-q8", MinVRAM: "10000GiB"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		)

		// No machine clears a 10000GiB floor, so this asserts the base is the
		// last resort and that missing every candidate is not an error.
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))
	})

	It("installs a fitting candidate's payload under the entry's own name", func() {
		newGallery(
			withCandidates(entry("qwen3-8b-q4", "base-backend"),
				gallery.Candidate{Model: "qwen3-8b-q8", MinVRAM: "0GiB"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		)

		// A 0GiB floor is met by every machine, so this asserts the upgrade wins
		// over the base and lands under the base's name rather than its own.
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("upgrade-backend"))
		_, err := os.Stat(filepath.Join(tempdir, "qwen3-8b-q8.yaml"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "the upgrade must not be installed under its own name")
	})

	It("round-trips the resolution record to disk under the entry's name", func() {
		// The upgrade is described by url so its payload carries its own name.
		// Without the entry-name overlay the record persists as "qwen3-8b-q8",
		// and the stable name the entry exists to provide is lost the moment
		// anything reads the record back.
		newGallery(
			withCandidates(urlEntry("qwen3-8b-q4", "base-backend"),
				gallery.Candidate{Model: "qwen3-8b-q8", MinVRAM: "0GiB"}),
			urlEntry("qwen3-8b-q8", "upgrade-backend"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.EntryName).To(Equal("qwen3-8b-q4"))
		Expect(record.ResolvedVariant).To(Equal("qwen3-8b-q8"))
		Expect(record.PinnedVariant).To(BeEmpty())
		Expect(record.Name).To(Equal("qwen3-8b-q4"))
		Expect(record.Description).To(Equal("entry qwen3-8b-q4"))
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("upgrade-backend"))
	})

	It("records a pin and honors it on a plain reinstall", func() {
		newGallery(
			withCandidates(entry("qwen3-8b-q4", "base-backend"),
				gallery.Candidate{Model: "qwen3-8b-q8", MinVRAM: "0GiB"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q4"))).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-q4"))
		Expect(record.ResolvedVariant).To(Equal("qwen3-8b-q4"))
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))

		// No WithVariant this time: hardware resolution would take the upgrade,
		// so only the recalled pin can keep this on the base payload.
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))

		record, err = gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-q4"))
	})

	It("honors a pin recorded under a custom install name", func() {
		newGallery(
			withCandidates(entry("qwen3-8b-q4", "base-backend"),
				gallery.Candidate{Model: "qwen3-8b-q8", MinVRAM: "0GiB"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		)

		req := gallery.GalleryModel{}
		req.Name = "prod-llm"

		Expect(install("qwen3-8b-q4", req, gallery.WithVariant("qwen3-8b-q4"))).To(Succeed())

		// The pin is written under the installed name, so the recall must read it
		// back under that name too and not under the gallery entry's name.
		record, err := gallery.GetLocalModelConfiguration(tempdir, "prod-llm")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-q4"))
		Expect(record.EntryName).To(Equal("qwen3-8b-q4"))
		Expect(installedBackend("prod-llm")).To(Equal("base-backend"))

		Expect(install("qwen3-8b-q4", req)).To(Succeed())
		Expect(installedBackend("prod-llm")).To(Equal("base-backend"))
	})

	It("writes each declared url only once into the persisted gallery file", func() {
		base := withCandidates(entry("qwen3-8b-q4", "base-backend"),
			gallery.Candidate{Model: "qwen3-8b-q8", MinVRAM: "0GiB"})
		base.URLs = []string{"https://example.invalid/qwen3"}
		newGallery(base, entry("qwen3-8b-q8", "upgrade-backend"))

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.URLs).To(ConsistOf("https://example.invalid/qwen3"))
	})
})

var _ = Describe("legacy client compatibility", func() {
	It("keeps every entry that declares candidates installable by clients that ignore them", func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
		Expect(err).ToNot(HaveOccurred())

		// Parse exactly as an older LocalAI release would: non-strictly, with
		// no knowledge of the candidates key. Such a client installs whatever
		// payload the entry carries directly, so the entry must carry one.
		var legacy []struct {
			Name       string         `yaml:"name"`
			URL        string         `yaml:"url"`
			ConfigFile map[string]any `yaml:"config_file"`
			Files      []gallery.File `yaml:"files"`
			Overrides  map[string]any `yaml:"overrides"`
		}
		Expect(yaml.Unmarshal(data, &legacy)).To(Succeed())

		var current []gallery.GalleryModel
		Expect(yaml.Unmarshal(data, &current)).To(Succeed())

		legacyByName := map[string]int{}
		for i, e := range legacy {
			legacyByName[e.Name] = i
		}

		withCandidates := 0
		for _, e := range current {
			if !e.HasCandidates() {
				continue
			}
			withCandidates++

			i, ok := legacyByName[e.Name]
			Expect(ok).To(BeTrue(), "entry %q vanished under a legacy parse", e.Name)
			old := legacy[i]
			// An entry whose payload lived only in its candidates would install
			// to nothing on every released LocalAI, which is precisely what
			// making the entry itself the base candidate exists to prevent.
			Expect(old.URL != "" || len(old.ConfigFile) > 0).To(BeTrue(),
				"entry %q carries no payload of its own, so older clients would install nothing", e.Name)
		}
		Expect(withCandidates).To(BeNumerically(">", 0),
			"expected at least one entry declaring candidates in the gallery index")
	})
})
