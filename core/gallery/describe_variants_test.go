package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("DescribeVariants", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	newModel := func(name, backend string) *gallery.GalleryModel {
		m := &gallery.GalleryModel{}
		m.Name = name
		m.Backend = backend
		return m
	}

	var models []*gallery.GalleryModel
	var base *gallery.GalleryModel
	// probed records every entry the probe was asked about, so a spec can
	// assert on the ABSENCE of a round trip and not merely on the result.
	var probed []string

	// probing builds an env whose sizes are injected rather than measured, so
	// these specs pin exact footprints without reaching the network.
	probing := func(available uint64, sizes map[string]uint64) gallery.ResolveEnv {
		return gallery.ResolveEnv{
			AvailableMemory:   available,
			BackendCompatible: func(string) bool { return true },
			ProbeMemory: func(target *gallery.GalleryModel) uint64 {
				probed = append(probed, target.Name)
				return sizes[target.Name]
			},
		}
	}

	byName := func(view *gallery.EntryVariants, name string) gallery.VariantView {
		GinkgoHelper()
		for _, v := range view.Variants {
			if v.Model == name {
				return v
			}
		}
		Fail("no variant named " + name + " in the reported view")
		return gallery.VariantView{}
	}

	BeforeEach(func() {
		probed = nil
		big := newModel("qwen3-8b-vllm-awq", "vllm")
		small := newModel("qwen3-8b-gguf-q8", "llama-cpp")
		base = newModel("qwen3-8b-gguf-q4", "llama-cpp")
		base.Variants = []gallery.Variant{
			{Model: "qwen3-8b-vllm-awq"},
			{Model: "qwen3-8b-gguf-q8"},
		}
		models = []*gallery.GalleryModel{big, small, base}
	})

	Describe("an entry that declares no variants", func() {
		It("reports nothing and issues no probe at all", func() {
			// This is the performance contract of the gallery listing: the
			// listing walks over a thousand entries and virtually none of them
			// declare variants, so the ordinary entry must cost zero round
			// trips. Asserting on `probed` rather than on timing makes that
			// contract testable.
			plain := newModel("plain-entry", "llama-cpp")
			models = append(models, plain)

			view, err := gallery.DescribeVariants(models, plain, probing(gib(24), map[string]uint64{
				"plain-entry": gib(4),
			}))

			Expect(err).ToNot(HaveOccurred())
			Expect(view).To(BeNil())
			Expect(probed).To(BeEmpty())
		})

		It("reports nothing for a nil entry rather than panicking", func() {
			view, err := gallery.DescribeVariants(models, nil, probing(gib(24), nil))
			Expect(err).ToNot(HaveOccurred())
			Expect(view).To(BeNil())
			Expect(probed).To(BeEmpty())
		})
	})

	Describe("an entry that declares variants", func() {
		It("reports every declared variant plus the entry's own build", func() {
			view, err := gallery.DescribeVariants(models, base, probing(gib(24), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
			Expect(err).ToNot(HaveOccurred())

			names := []string{}
			for _, v := range view.Variants {
				names = append(names, v.Model)
			}
			// The base is listed so a picker can offer "decline the upgrade",
			// which is a real choice an operator makes.
			Expect(names).To(ConsistOf("qwen3-8b-vllm-awq", "qwen3-8b-gguf-q8", "qwen3-8b-gguf-q4"))
			Expect(byName(view, "qwen3-8b-gguf-q4").IsBase).To(BeTrue())
			Expect(byName(view, "qwen3-8b-vllm-awq").IsBase).To(BeFalse())
		})

		It("reports the backend of the referenced entry, not of the declaring one", func() {
			// A picker renders this, and it is also the reason a variant can be
			// unavailable on a host with ample memory.
			view, err := gallery.DescribeVariants(models, base, probing(gib(24), nil))
			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").Backend).To(Equal("vllm"))
			Expect(byName(view, "qwen3-8b-gguf-q8").Backend).To(Equal("llama-cpp"))
		})

		It("reports the probed size of each variant", func() {
			view, err := gallery.DescribeVariants(models, base, probing(gib(24), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").MemoryBytes).To(Equal(gib(20)))
			Expect(byName(view, "qwen3-8b-gguf-q8").MemoryBytes).To(Equal(gib(9)))
		})

		It("reports a size it could not determine as unknown rather than as free", func() {
			// Zero on the wire means unknown. Rendering it as a zero-byte model
			// would advertise the largest download on offer as costless.
			view, err := gallery.DescribeVariants(models, base, probing(gib(24), map[string]uint64{}))
			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").MemoryBytes).To(Equal(uint64(0)))
			Expect(byName(view, "qwen3-8b-vllm-awq").Fits).To(BeTrue())
		})

		It("marks a variant too large for this host as not fitting", func() {
			view, err := gallery.DescribeVariants(models, base, probing(gib(10), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").Fits).To(BeFalse())
			Expect(byName(view, "qwen3-8b-gguf-q8").Fits).To(BeTrue())
		})

		It("marks a variant whose backend cannot run here as not fitting, however much memory there is", func() {
			env := probing(gib(1024), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			})
			env.BackendCompatible = func(backend string) bool { return backend != "vllm" }

			view, err := gallery.DescribeVariants(models, base, env)
			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").Fits).To(BeFalse())
			Expect(byName(view, "qwen3-8b-gguf-q8").Fits).To(BeTrue())
		})

		It("always reports the entry's own build as fitting", func() {
			// The base is exempt from every filter and always installs, so
			// showing it as unavailable would misdescribe it. Neither a hostile
			// backend gate nor a zero memory budget may change that.
			env := probing(0, map[string]uint64{})
			env.BackendCompatible = func(string) bool { return false }

			view, err := gallery.DescribeVariants(models, base, env)
			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-gguf-q4").Fits).To(BeTrue())
		})

		It("surfaces a variant that references an entry no gallery declares", func() {
			base.Variants = []gallery.Variant{{Model: "does-not-exist"}}
			_, err := gallery.DescribeVariants(models, base, probing(gib(24), nil))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does-not-exist"))
		})
	})

	Describe("the reported auto-selection", func() {
		// These are the specs that keep a picker honest: what the listing shows
		// as the default must be what installing with no variant actually does.
		// They assert against ResolveVariant rather than against a hardcoded
		// name, so a change to the selection rules cannot make the two drift
		// without failing here.
		agreesWithInstall := func(env gallery.ResolveEnv) {
			GinkgoHelper()
			view, err := gallery.DescribeVariants(models, base, env)
			Expect(err).ToNot(HaveOccurred())

			_, chosen, err := gallery.ResolveVariant(models, base, env, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(view.AutoSelected).To(Equal(chosen.Model))
		}

		It("matches what installing would pick when everything fits", func() {
			agreesWithInstall(probing(gib(64), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
		})

		It("matches what installing would pick when only the smaller variant fits", func() {
			agreesWithInstall(probing(gib(12), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
		})

		It("matches what installing would pick when nothing fits and the base wins", func() {
			agreesWithInstall(probing(gib(2), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
		})

		It("names the largest fitting variant, not the first authored", func() {
			// Pinned separately from agreesWithInstall so that a mutation making
			// BOTH functions pick the wrong variant still fails a spec.
			view, err := gallery.DescribeVariants(models, base, probing(gib(64), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
			Expect(err).ToNot(HaveOccurred())
			Expect(view.AutoSelected).To(Equal("qwen3-8b-vllm-awq"))
		})

		It("matches what installing would pick when the host prefers an engine", func() {
			// The picker and the installer both have to apply engine
			// preference, or a Mac would be shown the GGUF build and handed the
			// MLX one. Asserting the agreement AND the name pins both halves.
			mlx := newModel("qwen3-8b-mlx-4bit", "mlx")
			models = append(models, mlx)
			base.Variants = append(base.Variants, gallery.Variant{Model: "qwen3-8b-mlx-4bit"})

			env := probing(gib(64), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
				"qwen3-8b-mlx-4bit": gib(5),
			})
			// Engine names as SystemState.EnginePreferenceTokens reports them for
			// metal. Build tags would match no gallery `backend:` value here.
			env.EnginePreference = []string{"mlx", "llama-cpp"}

			agreesWithInstall(env)

			view, err := gallery.DescribeVariants(models, base, env)
			Expect(err).ToNot(HaveOccurred())
			Expect(view.AutoSelected).To(Equal("qwen3-8b-mlx-4bit"))
		})

		It("matches what installing would pick when the host prefers vLLM", func() {
			// The NVIDIA rule the user asked for, checked through the listing so
			// the picker and the installer cannot drift on it. The GGUF build is
			// deliberately the LARGER one, so only preference can produce this
			// answer: size alone would name the llama.cpp build.
			env := probing(gib(64), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(9),
				"qwen3-8b-gguf-q8":  gib(20),
			})
			env.EnginePreference = []string{"vllm", "sglang", "llama-cpp"}

			agreesWithInstall(env)

			view, err := gallery.DescribeVariants(models, base, env)
			Expect(err).ToNot(HaveOccurred())
			Expect(view.AutoSelected).To(Equal("qwen3-8b-vllm-awq"))
		})

		It("names the entry itself when no variant fits", func() {
			view, err := gallery.DescribeVariants(models, base, probing(gib(2), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(20),
				"qwen3-8b-gguf-q8":  gib(9),
			}))
			Expect(err).ToNot(HaveOccurred())
			Expect(view.AutoSelected).To(Equal("qwen3-8b-gguf-q4"))
		})
	})

	Describe("the facts that tell two builds of one model apart", func() {
		// servedAs points an entry at a weight file the way the gallery does,
		// so the reported quantization comes from the same field the installer
		// hands the backend.
		servedAs := func(m *gallery.GalleryModel, filename string) {
			m.Overrides = map[string]any{
				"parameters": map[string]any{"model": filename},
			}
		}

		It("reports each variant's quantization from the entry it references", func() {
			// The whole point of the field: these three rows share a backend
			// and sit within a gigabyte of each other, so quantization is the
			// only thing a user can actually choose on.
			servedAs(models[0], "models/Qwen3-8B-AWQ-4bit.safetensors")
			models[0].Backend = "llama-cpp"
			servedAs(models[1], "models/Qwen3-8B-Q8_0.gguf")
			servedAs(base, "models/Qwen3-8B-Q4_K_M.gguf")

			view, err := gallery.DescribeVariants(models, base, probing(gib(64), map[string]uint64{}))

			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").Quantization).To(Equal("4BIT"))
			Expect(byName(view, "qwen3-8b-gguf-q8").Quantization).To(Equal("Q8_0"))
			// The base is described from the entry's own payload, not skipped:
			// it is a selectable build like any other.
			Expect(byName(view, "qwen3-8b-gguf-q4").Quantization).To(Equal("Q4_K_M"))
		})

		It("leaves the quantization unset when the referenced entry names none", func() {
			// A vLLM build served from a directory of safetensors declares no
			// format. The field must be absent rather than empty-but-present,
			// so a client renders "unknown" instead of a blank cell.
			servedAs(models[0], "models/Qwen3-8B/")
			servedAs(base, "models/Qwen3-8B-Q4_K_M.gguf")

			view, err := gallery.DescribeVariants(models, base, probing(gib(64), map[string]uint64{}))

			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").Quantization).To(BeEmpty())
			Expect(byName(view, "qwen3-8b-gguf-q4").Quantization).To(Equal("Q4_K_M"))
		})

		It("names the serving features a build declares, best first", func() {
			models[0].Tags = []string{"llm", "MTP", "gguf", "dflash"}

			env := probing(gib(64), map[string]uint64{})
			env.ServingFeaturePreference = []string{"dflash", "mtp"}

			view, err := gallery.DescribeVariants(models, base, env)

			Expect(err).ToNot(HaveOccurred())
			// Preference order, not the author's order, and case-folded the way
			// the ranker folds it: "MTP" and "mtp" declare one feature.
			Expect(byName(view, "qwen3-8b-vllm-awq").Features).To(Equal([]string{"dflash", "mtp"}))
			// A tag outside the vocabulary is not a serving feature.
			Expect(byName(view, "qwen3-8b-gguf-q8").Features).To(BeEmpty())
		})

		It("agrees with the ranking: the build shown as faster is the one selection rewards", func() {
			// The contract that keeps the badge honest. Both builds fit and are
			// runnable, so only the serving feature can decide, and the variant
			// carrying the reported feature must be the one auto-selected.
			models[1].Tags = []string{"dflash"}

			env := probing(gib(64), map[string]uint64{
				"qwen3-8b-vllm-awq": gib(9),
				"qwen3-8b-gguf-q8":  gib(9),
			})
			env.ServingFeaturePreference = []string{"dflash", "mtp"}

			view, err := gallery.DescribeVariants(models, base, env)

			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-gguf-q8").Features).To(Equal([]string{"dflash"}))
			Expect(view.AutoSelected).To(Equal("qwen3-8b-gguf-q8"))
		})

		It("reports no features on a host with no serving feature vocabulary", func() {
			// An env with no preference list ranks every build equally on this
			// axis, so claiming a speed advantage would describe an advantage
			// nothing acted on.
			models[0].Tags = []string{"dflash"}

			view, err := gallery.DescribeVariants(models, base, probing(gib(64), map[string]uint64{}))

			Expect(err).ToNot(HaveOccurred())
			Expect(byName(view, "qwen3-8b-vllm-awq").Features).To(BeEmpty())
		})
	})
})
