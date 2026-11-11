package config

import (
	"os"
	"path/filepath"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config/harnessctx"
)

// These specs cover the window an unset context_size resolves to (the ladder
// of the chat target) and the rule that a value somebody set is never touched.

// ggufHeaderFor builds the snapshot the defaults path reads, the way
// readGGUFHeader does, but with an injected per-context footprint so no real
// model is needed.
func ggufHeaderFor(arch string, trained int, footprint func(ctx int) uint64) ggufHeader {
	h := ggufHeader{Name: "fixture", Architecture: arch, TrainedContext: trained, MaxContextLength: trained}
	h.footprints = map[int]uint64{}
	for _, target := range []int{harnessctx.RecommendedContext, DefaultAutoContextSize} {
		c := min(trained, target)
		h.footprints[c] = footprint(c)
	}
	for _, c := range contextFitCandidates {
		h.footprints[c] = footprint(c)
	}
	return h
}

var _ = Describe("Auto context for the chat agent", func() {
	const gib = uint64(1) << 30

	var (
		origVRAM     func() uint64
		origEstimate func(f *gguf.GGUFFile, ctx int) uint64
	)

	BeforeEach(func() {
		GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "false")
		origVRAM, origEstimate = perDeviceVRAM, estimateContextVRAM
	})
	AfterEach(func() { perDeviceVRAM, estimateContextVRAM = origVRAM, origEstimate })

	// kvPerToken makes the footprint grow with the window: base weights plus
	// a KV cache of kv bytes per token.
	kvPerToken := func(base uint64, kv uint64) func(int) uint64 {
		return func(ctx int) uint64 { return base + uint64(ctx)*kv }
	}

	DescribeTable("the ladder of a model that serves chats",
		func(trained int, vramGiB uint64, footprint func(int) uint64, want int) {
			perDeviceVRAM = func() uint64 { return vramGiB * gib }
			Expect(autoContextSize(footprint, nil, trained, harnessctx.RecommendedContext)).To(Equal(want))
		},
		Entry("long-context model on a large card takes the recommended window",
			1048576, uint64(80), kvPerToken(gib, 1<<10), 32768),
		Entry("unknown VRAM keeps the recommended window",
			262144, uint64(0), kvPerToken(100*gib, 1<<20), 32768),
		Entry("trained max below the target is kept",
			12288, uint64(80), kvPerToken(gib, 1<<10), 12288),
		Entry("trained max below the floor is kept as is",
			2048, uint64(80), kvPerToken(gib, 1<<10), 2048),
		// 4 GiB of weights and 160 KiB of KV per token. On a 10 GiB card 32768
		// needs 9 GiB (10.8 with headroom, does not fit) and 24576 needs
		// 7.75 GiB (9.3, fits).
		Entry("steps down to 24576 when 32768 does not fit",
			1048576, uint64(10), kvPerToken(4*gib, 160<<10), 24576),
		// 8 GiB card: 24576 needs 9.3 GiB with headroom, no; 16384 needs
		// 6.5 GiB (7.8), fits.
		Entry("steps down to 16384 when 24576 does not fit",
			1048576, uint64(8), kvPerToken(4*gib, 160<<10), 16384),
		// 7 GiB card: 16384 needs 7.8 GiB with headroom, no; 8192 needs
		// 5.25 GiB (6.3), fits.
		Entry("goes below the chat minimum only when 16384 does not fit",
			1048576, uint64(7), kvPerToken(4*gib, 160<<10), 8192),
		Entry("reaches 4096 and then the floor",
			1048576, uint64(1), kvPerToken(100*gib, 128<<10), DefaultContextSize),
	)

	It("takes the first rung that fits, never skipping a larger one", func() {
		perDeviceVRAM = func() uint64 { return 10 * gib }
		fp := kvPerToken(4*gib, 160<<10)
		got := autoContextSize(fp, nil, 1048576, harnessctx.RecommendedContext)
		for _, larger := range contextFitCandidates {
			if larger > got {
				Expect(contextFitsVRAM(fp(larger), 10*gib)).To(BeFalse(), "%d fits but %d was chosen", larger, got)
			}
		}
	})

	It("keeps the old cap for a model that does not serve chats", func() {
		perDeviceVRAM = func() uint64 { return 80 * gib }
		fp := kvPerToken(gib, 1<<10)
		Expect(autoContextSize(fp, nil, 1048576, DefaultAutoContextSize)).To(Equal(DefaultAutoContextSize))
	})

	Describe("which models get the chat target", func() {
		yes := true
		DescribeTable("ServesChat",
			func(cfg ModelConfig, arch string, want bool) {
				Expect(cfg.ServesChat(arch)).To(Equal(want))
				if want {
					Expect(cfg.AutoContextTarget(arch)).To(Equal(harnessctx.RecommendedContext))
				} else {
					Expect(cfg.AutoContextTarget(arch)).To(Equal(DefaultAutoContextSize))
				}
			},
			Entry("a plain LLM", ModelConfig{}, "llama", true),
			Entry("a chat model declaring its usecases", ModelConfig{KnownUsecaseStrings: []string{"chat", "completion"}}, "qwen3", true),
			Entry("an embedding model by flag", ModelConfig{Embeddings: &yes}, "llama", false),
			Entry("an embedding model by architecture", ModelConfig{}, "nomic-bert", false),
			Entry("a reranker by flag", ModelConfig{LLMConfig: LLMConfig{Reranking: &yes}}, "qwen3", false),
			Entry("a model declared for embeddings only", ModelConfig{KnownUsecaseStrings: []string{"embeddings"}}, "llama", false),
			Entry("a model declared for rerank only", ModelConfig{KnownUsecaseStrings: []string{"rerank"}}, "qwen3", false),
			Entry("a score-only model", ModelConfig{KnownUsecaseStrings: []string{"score"}}, "llama", false),
			Entry("a TTS model declared by usecase", ModelConfig{KnownUsecaseStrings: []string{"tts"}}, "llama", false),
			Entry("a transcription model declared by usecase", ModelConfig{KnownUsecaseStrings: []string{"transcript"}}, "llama", false),
			Entry("an image model declared by usecase", ModelConfig{KnownUsecaseStrings: []string{"image"}}, "llama", false),
		)
	})

	Describe("guessGGUFFromFile", func() {
		roomy := func() { perDeviceVRAM = func() uint64 { return 80 * gib } }

		It("sizes a chat model to the recommended window and records that it was automatic", func() {
			roomy()
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, ggufHeaderFor("llama", 131072, kvPerToken(gib, 1<<10)), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(32768)))
			Expect(cfg.ContextAutoSized).To(BeTrue())
		})

		It("does not raise the window of an embedding model", func() {
			roomy()
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, ggufHeaderFor("nomic-bert", 131072, kvPerToken(gib, 1<<10)), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultAutoContextSize)))
		})

		It("does not raise the window of a reranker declared by flag", func() {
			roomy()
			rr := true
			cfg := &ModelConfig{LLMConfig: LLMConfig{Reranking: &rr}}
			guessGGUFFromFile(cfg, ggufHeaderFor("qwen3", 131072, kvPerToken(gib, 1<<10)), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultAutoContextSize)))
		})

		It("caps at the trained maximum of a small model", func() {
			roomy()
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, ggufHeaderFor("llama", 4096, kvPerToken(gib, 1<<10)), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(4096)))
		})

		It("steps down through the VRAM-fit ladder", func() {
			perDeviceVRAM = func() uint64 { return 8 * gib }
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, ggufHeaderFor("llama", 131072, kvPerToken(4*gib, 160<<10)), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(16384)))
		})
	})

	// An explicit context_size is never overridden, even below the chat
	// minimum. The banner of the chat is how such a model is handled.
	Describe("an explicit context_size", func() {
		roomy := func() { perDeviceVRAM = func() uint64 { return 80 * gib } }
		fixture := func() ggufHeader { return ggufHeaderFor("llama", 131072, kvPerToken(gib, 1<<10)) }

		DescribeTable("survives the GGUF guess, below the harness minimum too",
			func(size int) {
				roomy()
				v := size
				cfg := &ModelConfig{LLMConfig: LLMConfig{ContextSize: &v}}
				guessGGUFFromFile(cfg, fixture(), 0)
				Expect(cfg.ContextSize).To(HaveValue(Equal(size)))
				Expect(cfg.ContextAutoSized).To(BeFalse())
			},
			Entry("2048", 2048),
			Entry("8192", 8192),
			Entry("a value above the recommended window", 65536),
		)

		It("keeps a gallery-style config's small value through load and defaults", func() {
			roomy()
			for _, size := range []int{2048, 8192} {
				var cfg ModelConfig
				doc := "name: gallery-model\nbackend: llama-cpp\ncontext_size: " + map[int]string{2048: "2048", 8192: "8192"}[size] + "\nparameters:\n  model: weights.gguf\n"
				Expect(yaml.Unmarshal([]byte(doc), &cfg)).To(Succeed())
				cfg.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(32768))
				guessGGUFFromFile(&cfg, fixture(), 0)
				Expect(cfg.ContextSize).To(HaveValue(Equal(size)))
			}
		})

		It("uses the global default only when the model leaves context_size unset", func() {
			roomy()
			// Unset: the global default applies, and the GGUF guess leaves it alone.
			unset := &ModelConfig{Name: "m"}
			unset.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(4096))
			Expect(unset.ContextSize).To(HaveValue(Equal(4096)))
			guessGGUFFromFile(unset, fixture(), 0)
			Expect(unset.ContextSize).To(HaveValue(Equal(4096)), "the global default must beat the automatic size")
			Expect(unset.ContextAutoSized).To(BeFalse())

			// Set: the model's own value wins over the global default.
			own := 12288
			set := &ModelConfig{Name: "m", LLMConfig: LLMConfig{ContextSize: &own}}
			set.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(4096))
			Expect(set.ContextSize).To(HaveValue(Equal(12288)))
		})

		It("keeps a value saved through the model editor when the loader reads it back", func() {
			roomy()
			dir := GinkgoT().TempDir()
			// The editor saves the fields the admin set as a plain YAML document.
			editor := "name: edited\nbackend: llama-cpp\ncontext_size: 6144\nparameters:\n  model: weights.gguf\n"
			Expect(os.WriteFile(filepath.Join(dir, "edited.yaml"), []byte(editor), 0o600)).To(Succeed())
			cl := NewModelConfigLoader(dir)
			cfg, err := cl.LoadModelConfigFileByName("edited", dir, LoadOptionContextSize(32768), ModelPath(dir))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ContextSize).To(HaveValue(Equal(6144)))
			guessGGUFFromFile(cfg, fixture(), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(6144)))
		})

		It("reads a context_size of 0 from the editor as unset, which is the one exception", func() {
			// 0 is the editor's placeholder, not a value (see SetDefaults): the
			// global default applies, as it does for a missing key.
			zero := 0
			cfg := &ModelConfig{Name: "m", LLMConfig: LLMConfig{ContextSize: &zero}}
			cfg.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(6144))
			Expect(cfg.ContextSize).To(HaveValue(Equal(6144)))
		})
	})
})
