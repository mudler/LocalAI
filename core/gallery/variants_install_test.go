package gallery_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"dario.cat/mergo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("GalleryModel variant declarations", func() {
	It("declares no variants when the key is absent", func() {
		m := gallery.GalleryModel{}
		m.Name = "plain"
		Expect(m.HasVariants()).To(BeFalse())
	})

	It("declares variants when the key is present", func() {
		m := gallery.GalleryModel{Variants: []gallery.Variant{{Model: "x"}}}
		Expect(m.HasVariants()).To(BeTrue())
	})

	It("parses a variant list", func() {
		var m gallery.GalleryModel
		err := yaml.Unmarshal([]byte(`
name: qwen3.6-27b
url: "github:example/repo/qwen3.6-27b.yaml@master"
variants:
  - model: qwen3.6-27b-mlx-8bit
  - model: qwen3.6-27b-gguf-q8
`), &m)
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Name).To(Equal("qwen3.6-27b"))
		Expect(m.URL).To(Equal("github:example/repo/qwen3.6-27b.yaml@master"))
		Expect(m.HasVariants()).To(BeTrue())
		Expect(m.Variants).To(HaveLen(2))
		// A variant is nothing but a name, which is the whole of the authoring
		// surface.
		Expect(m.Variants[0].Model).To(Equal("qwen3.6-27b-mlx-8bit"))
		Expect(m.Variants[1].Model).To(Equal("qwen3.6-27b-gguf-q8"))
	})
})

var _ = Describe("ResolveVariant", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	// runsEverything keeps these specs about resolution rather than about the
	// hardware of whatever machine runs them.
	runsEverything := func(string) bool { return true }

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
		upgrade.Backend = "vllm"
		// The base is an ordinary, complete entry that happens to list an
		// alternative build of itself, which is the whole point of the design.
		base = newModel("qwen3-8b-gguf-q4", "file://gguf.yaml", "Qwen3 8B Q4", "qwen.png")
		base.Backend = "llama-cpp"
		base.Tags = []string{"llm"}
		base.Variants = []gallery.Variant{{Model: "qwen3-8b-vllm-awq"}}
		models = []*gallery.GalleryModel{upgrade, base}
	})

	// The AWQ build measures 20GiB, so the specs below vary only the host and
	// read off which payload resolution lands on. The probe is injected rather
	// than performed, so nothing here reaches the network.
	env := func(memory uint64) gallery.ResolveEnv {
		return gallery.ResolveEnv{
			AvailableMemory:   memory,
			BackendCompatible: runsEverything,
			ProbeMemory: func(target *gallery.GalleryModel) uint64 {
				if target.Name == "qwen3-8b-vllm-awq" {
					return 20 * 1024 * 1024 * 1024
				}
				return 0
			},
		}
	}

	It("installs a fitting variant's payload under the entry's name", func() {
		resolved, variant, err := gallery.ResolveVariant(models, base, env(gib(24)), "")
		Expect(err).ToNot(HaveOccurred())
		Expect(variant.Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(resolved.Name).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://vllm.yaml"))
	})

	It("carries the referenced entry's backend into the compatibility check", func() {
		// The variant itself says nothing about hardware, so the backend can
		// only come from the entry it names. Rejecting exactly that backend must
		// therefore be enough to rule the variant out.
		resolved, variant, err := gallery.ResolveVariant(models, base, gallery.ResolveEnv{
			AvailableMemory:   gib(64),
			BackendCompatible: func(backend string) bool { return backend != "vllm" },
		}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("falls back to the entry's own payload when no variant fits", func() {
		resolved, variant, err := gallery.ResolveVariant(models, base, env(gib(8)), "")
		Expect(err).ToNot(HaveOccurred())
		Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("installs the entry on a host with no memory budget at all", func() {
		// The base carries no floor of its own precisely so this can never
		// refuse; an unreadable host reports 0 and must still get the entry.
		resolved, variant, err := gallery.ResolveVariant(models, base, env(0), "")
		Expect(err).ToNot(HaveOccurred())
		Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("strips the variant list from the resolved entry", func() {
		// A resolved entry is a concrete install target. Leaving the list on it
		// would let a second selection pass fire on an already-resolved entry.
		resolved, _, err := gallery.ResolveVariant(models, base, env(gib(8)), "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.HasVariants()).To(BeFalse())
	})

	Describe("the live size probe", func() {
		// The probe is injected rather than performed, so these specs pin an
		// exact size, or an exact failure, without reaching the network.
		probing := func(memory uint64, sizes map[string]uint64) gallery.ResolveEnv {
			e := env(memory)
			e.ProbeMemory = func(target *gallery.GalleryModel) uint64 { return sizes[target.Name] }
			return e
		}

		It("selects a variant the probe shows to fit", func() {
			resolved, variant, err := gallery.ResolveVariant(models, base,
				probing(gib(24), map[string]uint64{"qwen3-8b-vllm-awq": gib(20)}), "")
			Expect(err).ToNot(HaveOccurred())
			Expect(variant.Model).To(Equal("qwen3-8b-vllm-awq"))
			Expect(resolved.URL).To(Equal("file://vllm.yaml"))
		})

		It("rejects a variant the probe shows to be too large", func() {
			// Same variant, same host, only the probed size differs, so nothing
			// but the probe can account for the different answer.
			resolved, variant, err := gallery.ResolveVariant(models, base,
				probing(gib(24), map[string]uint64{"qwen3-8b-vllm-awq": gib(80)}), "")
			Expect(err).ToNot(HaveOccurred())
			Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
			Expect(resolved.URL).To(Equal("file://gguf.yaml"))
		})

		It("completes the install on the entry's own payload when no probe answers", func() {
			// A failed probe reports 0. That is an unknown, so nothing is filtered
			// out and above all the resolution does not fail: a network hiccup
			// must never be able to break an install. It must not be able to
			// change what gets installed either, so an unmeasurable variant does
			// not displace the payload the entry itself ships.
			resolved, variant, err := gallery.ResolveVariant(models, base,
				probing(gib(24), map[string]uint64{}), "")
			Expect(err).ToNot(HaveOccurred())
			Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
			Expect(resolved.URL).To(Equal("file://gguf.yaml"))
		})

		It("still installs the base when every probe fails and nothing else survives", func() {
			// The one variant this host could otherwise take is ruled out by its
			// backend and no probe answers, so selection has to terminate on the
			// base rather than on an error.
			e := probing(gib(24), map[string]uint64{})
			e.BackendCompatible = func(backend string) bool { return backend != "vllm" }

			resolved, variant, err := gallery.ResolveVariant(models, base, e, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
			Expect(resolved.URL).To(Equal("file://gguf.yaml"))
		})
	})

	It("presents the entry's metadata, not the variant's", func() {
		resolved, _, err := gallery.ResolveVariant(models, base, env(gib(24)), "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.Description).To(Equal("Qwen3 8B Q4"))
		Expect(resolved.Icon).To(Equal("qwen.png"))
		Expect(resolved.Tags).To(ConsistOf("llm"))
	})

	It("honors a pin naming the entry itself", func() {
		// The entry is the last resort of its own variant list, so its own name
		// has to be a usable pin: it is how an operator declines an upgrade
		// their hardware would otherwise take.
		resolved, variant, err := gallery.ResolveVariant(models, base, env(gib(24)), "qwen3-8b-gguf-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(variant.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.Name).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("honors a pin the hardware does not satisfy", func() {
		resolved, variant, err := gallery.ResolveVariant(models, base, env(gib(2)), "qwen3-8b-vllm-awq")
		Expect(err).ToNot(HaveOccurred())
		Expect(variant.Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(resolved.URL).To(Equal("file://vllm.yaml"))
	})

	It("detaches the resolved entry from the gallery's own maps and slices", func() {
		models[0].Overrides = map[string]any{"f16": true}
		models[0].AdditionalFiles = []gallery.File{{Filename: "a.bin"}}

		resolved, _, err := gallery.ResolveVariant(models, base, env(gib(24)), "")
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

		resolved, _, err := gallery.ResolveVariant(models, base, env(gib(8)), "")
		Expect(err).ToNot(HaveOccurred())

		resolved.Overrides["parameters"].(map[string]any)["model"] = "callers-choice.gguf"
		Expect(base.Overrides["parameters"]).To(HaveKeyWithValue("model", "q4.gguf"))
	})

	It("detaches nested override maps from the gallery's own entry", func() {
		models[0].Overrides = map[string]any{
			"parameters": map[string]any{"model": "real-variant.gguf"},
			"stopwords":  []any{"</s>"},
		}

		resolved, _, err := gallery.ResolveVariant(models, base, env(gib(24)), "")
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

		resolved, _, err := gallery.ResolveVariant(models, base, env(gib(24)), "")
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

	It("errors when a variant references a missing entry", func() {
		base.Variants = []gallery.Variant{{Model: "does-not-exist"}}
		_, _, err := gallery.ResolveVariant(models, base, env(gib(8)), "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does-not-exist"))
	})

	It("refuses a variant that declares variants of its own", func() {
		nested := newModel("nested", "file://nested.yaml", "", "")
		nested.Variants = []gallery.Variant{{Model: "qwen3-8b-vllm-awq"}}
		models = append(models, nested)
		base.Variants = []gallery.Variant{{Model: "nested"}}
		_, _, err := gallery.ResolveVariant(models, base, env(gib(8)), "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nested"))
	})

	It("surfaces a bad pin", func() {
		_, _, err := gallery.ResolveVariant(models, base, env(gib(24)), "nope")
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
	})
})

var _ = Describe("InstallModelFromGallery with variant entries", func() {
	var tempdir string
	var galleries []config.Gallery
	var systemState *system.SystemState
	// galleryRevision keeps every gallery this suite writes distinct from every
	// other, so the process-wide gallery cache cannot serve one spec's catalog
	// to another.
	galleryRevision := 0

	// Every entry is described with an inline config_file rather than a URL so
	// the whole install runs off the local filesystem with no network access.
	//
	// Each call writes a fresh path and a fresh gallery name because the gallery
	// listing is cached on the name and URL pair. A spec that edits its gallery
	// mid-flight would otherwise keep reading the version it started with, and
	// would silently prove nothing.
	newGallery := func(entries ...gallery.GalleryModel) {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		name := fmt.Sprintf("test-%d", galleryRevision)
		galleryRevision++
		galleryPath := filepath.Join(tempdir, name+".yaml")
		Expect(os.WriteFile(galleryPath, out, 0600)).To(Succeed())

		galleries = []config.Gallery{{Name: name, URL: "file://" + galleryPath}}
	}

	entry := func(name, backend string) gallery.GalleryModel {
		m := gallery.GalleryModel{ConfigFile: map[string]any{"backend": backend}}
		m.Name = name
		m.Description = "entry " + name
		return m
	}

	// sizedEntry declares its footprint through the entry's own size:, which is
	// the only way an author influences a variant's measured size. The estimator
	// prefers a declared size over its own guesswork and parses it locally, so
	// these specs pin an exact figure without touching the network.
	sizedEntry := func(name, backend, size string) gallery.GalleryModel {
		m := entry(name, backend)
		m.Size = size
		return m
	}

	// urlEntry describes an entry through a url rather than an inline
	// config_file. The distinction matters for the recorded name: the url branch
	// reads a name out of the fetched config, and that name is the referenced
	// entry's own, so only the entry-name overlay can keep the record under the
	// name the user asked for. The inline config_file branch seeds the name from
	// the already-renamed resolved entry and so cannot observe the overlay.
	urlEntry := func(name, backend, size string) gallery.GalleryModel {
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
		m.Size = size
		return m
	}

	// withVariants attaches alternative builds to an otherwise ordinary entry.
	// Where a spec needs a definite size it declares it on the referenced entry
	// with sizedEntry, in absolute terms rather than relative to the host, so
	// these specs assert on selection rather than on whatever memory the machine
	// running them happens to have.
	withVariants := func(m gallery.GalleryModel, variants ...gallery.Variant) gallery.GalleryModel {
		m.Variants = variants
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
		tempdir, err = os.MkdirTemp("", "variant-install")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempdir)).To(Succeed()) })

		systemState, err = system.GetSystemState(system.WithModelPath(tempdir))
		Expect(err).ToNot(HaveOccurred())
	})

	It("installs the entry's own payload when no variant fits the host", func() {
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			sizedEntry("qwen3-8b-q8", "upgrade-backend", "10000GiB"),
		)

		// No machine clears 10000GiB, so this asserts the base is the last
		// resort and that missing every variant is not an error.
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))
	})

	It("installs a fitting variant's payload under the entry's own name", func() {
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			sizedEntry("qwen3-8b-q8", "upgrade-backend", "16MiB"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("upgrade-backend"))
		_, err := os.Stat(filepath.Join(tempdir, "qwen3-8b-q8.yaml"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "the variant must not be installed under its own name")
	})

	It("completes the install when a variant's size cannot be determined", func() {
		// This runs the REAL probe against an entry that declares no size and no
		// weight files, which is exactly the shape a probe cannot answer for.
		// It must yield an unknown, not an error: the variant survives the
		// filter and the install completes, on the variant or on the base, but
		// never on a failure. Nothing here touches the network.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(BeElementOf("base-backend", "upgrade-backend"))
	})

	It("installs the largest fitting variant, not the first authored", func() {
		// Authored smallest-first with all three within reach, so first-match
		// would take the small one.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-small"},
				gallery.Variant{Model: "qwen3-8b-large"},
			),
			sizedEntry("qwen3-8b-small", "small-backend", "16MiB"),
			sizedEntry("qwen3-8b-large", "large-backend", "256MiB"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("large-backend"))
	})

	It("never installs a variant whose backend cannot run on this host", func() {
		if runtime.GOOS == "darwin" {
			Skip("mlx is a compatible backend on darwin, so it proves nothing here")
		}
		// The mlx entry is the only variant and fits trivially, so the real
		// SystemState.IsBackendCompatible rejecting mlx on a non-darwin host is
		// the only thing that can send this to the base.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-mlx"}),
			entry("qwen3-8b-mlx", "mlx"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))
	})

	It("round-trips the resolution record to disk under the entry's name", func() {
		// The variant is described by url so its payload carries its own name.
		// Without the entry-name overlay the record persists as "qwen3-8b-q8",
		// and the stable name the entry exists to provide is lost the moment
		// anything reads the record back.
		// The declared sizes make the upgrade the largest build that fits, so
		// resolution lands on the variant and the record it writes is the one
		// under test here.
		newGallery(
			withVariants(urlEntry("qwen3-8b-q4", "base-backend", "16MiB"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			urlEntry("qwen3-8b-q8", "upgrade-backend", "256MiB"),
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

	It("refuses a variant name the entry does not declare, naming what was asked for", func() {
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		)

		err := install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q6"))

		// Auto-selecting instead would report success while silently
		// installing something other than what the caller asked for, and a
		// typo'd variant name would be indistinguishable from a working one.
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
		Expect(err.Error()).To(ContainSubstring("qwen3-8b-q6"))
		Expect(filepath.Join(tempdir, "qwen3-8b-q4.yaml")).ToNot(BeAnExistingFile())
	})

	It("refuses a variant name when the entry declares no variants at all", func() {
		// The entry is installable and selection never runs for it, so without
		// an explicit refusal the requested variant would be dropped on the
		// floor and the install would look like it honored the choice.
		newGallery(entry("qwen3-8b-q4", "base-backend"))

		err := install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q8"))

		Expect(err).To(MatchError(gallery.ErrPinNotFound))
		Expect(err.Error()).To(ContainSubstring("qwen3-8b-q8"))
		Expect(filepath.Join(tempdir, "qwen3-8b-q4.yaml")).ToNot(BeAnExistingFile())
	})

	It("records a pin and honors it on a plain reinstall", func() {
		// The upgrade declares a size and the base does not, so auto-selection
		// genuinely prefers the upgrade here. Without that the second install
		// below would land on the base whether or not the pin was recalled, and
		// the spec would prove nothing.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			sizedEntry("qwen3-8b-q8", "upgrade-backend", "16MiB"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q4"))).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-q4"))
		Expect(record.ResolvedVariant).To(Equal("qwen3-8b-q4"))
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))

		// No WithVariant this time: hardware selection would take the upgrade,
		// so only the recalled pin can keep this on the base payload.
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))

		record, err = gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-q4"))
	})

	It("honors a pin recorded under a custom install name", func() {
		// Sized as above so auto-selection would take the upgrade, which is what
		// makes the recall observable in the second install.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			sizedEntry("qwen3-8b-q8", "upgrade-backend", "16MiB"),
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

	It("re-selects automatically when a recorded pin no longer exists in the gallery", func() {
		// A pin is recorded, then the gallery is edited to rename the build it
		// named. Without dropping the stale pin every later reinstall and every
		// upgrade of this model fails forever, and the only remedy is deleting a
		// dotfile the user has never heard of.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			sizedEntry("qwen3-8b-q8", "upgrade-backend", "16MiB"),
		)
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q8"))).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(Equal("qwen3-8b-q8"))

		// The gallery edit: the same build under a new name.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8-v2"}),
			sizedEntry("qwen3-8b-q8-v2", "renamed-backend", "16MiB"),
		)

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())
		// Auto-selection ran: the renamed build is the largest that fits.
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("renamed-backend"))

		// The stale pin is cleared rather than carried forward, so the next
		// install does not have to rediscover that it is unusable.
		record, err = gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.PinnedVariant).To(BeEmpty())
		Expect(record.ResolvedVariant).To(Equal("qwen3-8b-q8-v2"))
	})

	It("still refuses a variant the caller names on this request, recorded pin or not", func() {
		// The caller-supplied pin and the recalled one must fail differently.
		// This one is a request that cannot be honored, so reporting success
		// for it would make a typo indistinguishable from a working choice.
		newGallery(
			withVariants(entry("qwen3-8b-q4", "base-backend"),
				gallery.Variant{Model: "qwen3-8b-q8"}),
			sizedEntry("qwen3-8b-q8", "upgrade-backend", "16MiB"),
		)
		Expect(install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q8"))).To(Succeed())

		err := install("qwen3-8b-q4", gallery.GalleryModel{}, gallery.WithVariant("qwen3-8b-q6"))
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
		Expect(err.Error()).To(ContainSubstring("qwen3-8b-q6"))
	})

	It("writes each declared url only once into the persisted gallery file", func() {
		base := withVariants(entry("qwen3-8b-q4", "base-backend"),
			gallery.Variant{Model: "qwen3-8b-q8"})
		base.URLs = []string{"https://example.invalid/qwen3"}
		newGallery(base, entry("qwen3-8b-q8", "upgrade-backend"))

		Expect(install("qwen3-8b-q4", gallery.GalleryModel{})).To(Succeed())

		record, err := gallery.GetLocalModelConfiguration(tempdir, "qwen3-8b-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(record.URLs).To(ConsistOf("https://example.invalid/qwen3"))
	})
})

var _ = Describe("legacy client compatibility", func() {
	It("keeps every entry that declares variants installable by clients that ignore them", func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
		Expect(err).ToNot(HaveOccurred())

		// Parse exactly as an older LocalAI release would: non-strictly, with
		// no knowledge of the variants key. Such a client installs whatever
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

		withVariants := 0
		for _, e := range current {
			if !e.HasVariants() {
				continue
			}
			withVariants++

			i, ok := legacyByName[e.Name]
			Expect(ok).To(BeTrue(), "entry %q vanished under a legacy parse", e.Name)
			old := legacy[i]
			// An entry whose payload lived only in its variants would install to
			// nothing on every released LocalAI, which is precisely what making
			// the entry itself the last resort exists to prevent.
			Expect(old.URL != "" || len(old.ConfigFile) > 0).To(BeTrue(),
				"entry %q carries no payload of its own, so older clients would install nothing", e.Name)
		}
		Expect(withVariants).To(BeNumerically(">", 0),
			"expected at least one entry declaring variants in the gallery index")
	})
})
