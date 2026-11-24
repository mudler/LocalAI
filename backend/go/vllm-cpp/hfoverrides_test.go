package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("hf_overrides", func() {
	var modelDir string
	const originalConfig = `{"architectures":["Qwen3_5ForConditionalGeneration"],"hidden_size":2560,"text_config":{"num_hidden_layers":36}}`

	BeforeEach(func() {
		modelDir = GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(originalConfig), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("weights"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte("{}"), 0o644)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(modelDir, "tokenizer"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelDir, "tokenizer", "vocab.json"), []byte("{}"), 0o644)).To(Succeed())
	})

	Describe("parsing", func() {
		It("reads a YAML-nested object from engine_args as a JSON document", func() {
			lo := parseOptions(&pb.ModelOptions{
				EngineArgs: `{"hf_overrides":{"architectures":["Tev1Model"]},"max_num_seqs":2}`,
			})
			Expect(lo.hfOverrides).To(MatchJSON(`{"architectures":["Tev1Model"]}`))
			Expect(lo.maxNumSeqs).To(Equal(int32(2)))
		})

		It("accepts a pre-encoded JSON string", func() {
			lo := parseOptions(&pb.ModelOptions{
				EngineArgs: `{"hf_overrides":"{\"architectures\":[\"Tev1Model\"]}"}`,
			})
			Expect(lo.hfOverrides).To(MatchJSON(`{"architectures":["Tev1Model"]}`))
		})
	})

	Describe("config overlay", func() {
		It("writes the merged config.json and symlinks every other entry to the original", func() {
			overlay, err := newConfigOverlay(modelDir, `{"architectures":["Tev1Model"],"new_key":1}`)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(os.RemoveAll, overlay)
			Expect(overlay).ToNot(Equal(modelDir))

			raw, err := os.ReadFile(filepath.Join(overlay, "config.json"))
			Expect(err).ToNot(HaveOccurred())
			Expect(raw).To(MatchJSON(`{"architectures":["Tev1Model"],"hidden_size":2560,"text_config":{"num_hidden_layers":36},"new_key":1}`))
			info, err := os.Lstat(filepath.Join(overlay, "config.json"))
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode() & os.ModeSymlink).To(BeZero())

			for _, name := range []string{"model.safetensors", "tokenizer.json", "tokenizer"} {
				target, err := os.Readlink(filepath.Join(overlay, name))
				Expect(err).ToNot(HaveOccurred(), name)
				Expect(target).To(Equal(filepath.Join(modelDir, name)))
			}
			// The subdir resolves through the link, so a tokenizer/ fallback
			// in the engine still finds its files.
			Expect(filepath.Join(overlay, "tokenizer", "vocab.json")).To(BeARegularFile())

			entries, err := os.ReadDir(overlay)
			Expect(err).ToNot(HaveOccurred())
			Expect(entries).To(HaveLen(4))
		})

		It("leaves the original config.json untouched", func() {
			overlay, err := newConfigOverlay(modelDir, `{"architectures":["Tev1Model"]}`)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(os.RemoveAll, overlay)

			raw, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(raw)).To(Equal(originalConfig))
		})

		It("is removed by Free", func() {
			overlay, err := newConfigOverlay(modelDir, `{"architectures":["Tev1Model"]}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(overlay).To(BeADirectory())

			v := &VllmCpp{overlayDir: overlay}
			Expect(v.Free()).To(Succeed())
			Expect(overlay).ToNot(BeAnExistingFile())
			Expect(v.overlayDir).To(BeEmpty())
			// The originals the links pointed at survive the cleanup.
			Expect(filepath.Join(modelDir, "model.safetensors")).To(BeARegularFile())
			Expect(filepath.Join(modelDir, "tokenizer", "vocab.json")).To(BeARegularFile())
		})

		DescribeTable("refuses bad input",
			func(overrides string, useFile bool, substr string) {
				target := modelDir
				if useFile {
					target = filepath.Join(modelDir, "model.safetensors")
				}
				overlay, err := newConfigOverlay(target, overrides)
				Expect(err).To(MatchError(ContainSubstring(substr)))
				Expect(overlay).To(BeEmpty())
			},
			Entry("a JSON array", `["Tev1Model"]`, false, "must be a JSON object"),
			Entry("a JSON scalar", `5`, false, "must be a JSON object"),
			Entry("unparseable JSON", `{"architectures":`, false, "must be a JSON object"),
			Entry("a model that is not a directory", `{"architectures":["Tev1Model"]}`, true, "model directory"),
		)

		It("refuses a directory without config.json", func() {
			Expect(os.Remove(filepath.Join(modelDir, "config.json"))).To(Succeed())
			_, err := newConfigOverlay(modelDir, `{"architectures":["Tev1Model"]}`)
			Expect(err).To(MatchError(ContainSubstring("config.json")))
		})

		It("does not leave a half-built overlay behind on failure", func() {
			Expect(os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("not json"), 0o644)).To(Succeed())
			before, _ := filepath.Glob(filepath.Join(os.TempDir(), "vllm-cpp-hf-overrides-*"))
			_, err := newConfigOverlay(modelDir, `{"architectures":["Tev1Model"]}`)
			Expect(err).To(HaveOccurred())
			after, _ := filepath.Glob(filepath.Join(os.TempDir(), "vllm-cpp-hf-overrides-*"))
			Expect(after).To(ConsistOf(before))
		})
	})

	It("keeps the merged document a valid object when overrides replace a nested key", func() {
		overlay, err := newConfigOverlay(modelDir, `{"text_config":{"num_hidden_layers":2}}`)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, overlay)
		raw, err := os.ReadFile(filepath.Join(overlay, "config.json"))
		Expect(err).ToNot(HaveOccurred())
		var doc map[string]any
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		Expect(doc["text_config"]).To(Equal(map[string]any{"num_hidden_layers": float64(2)}))
	})
})
