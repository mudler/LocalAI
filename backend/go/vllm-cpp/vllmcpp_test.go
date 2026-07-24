package main

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVllmCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vllm-cpp suite")
}

// The Go POD mirrors must match the C struct layout of vllm.h (ABI v2)
// byte-for-byte: these offsets are the C offsets on LP64 (linux/darwin
// amd64+arm64). A failure here means govllmcpp.go drifted from vllm.h.
var _ = Describe("C ABI struct mirrors", func() {
	It("cModelParams matches vllm_model_params", func() {
		var p cModelParams
		Expect(unsafe.Offsetof(p.ModelPath)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(p.TokenizerConfigPath)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(p.BlockSize)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(p.NumBlocks)).To(Equal(uintptr(20)))
		Expect(unsafe.Offsetof(p.MaxModelLen)).To(Equal(uintptr(24)))
		Expect(unsafe.Offsetof(p.MaxNumSeqs)).To(Equal(uintptr(28)))
		Expect(unsafe.Offsetof(p.ToolParser)).To(Equal(uintptr(32)))
		Expect(unsafe.Offsetof(p.ReasoningParser)).To(Equal(uintptr(40)))
		Expect(unsafe.Sizeof(p)).To(Equal(uintptr(48)))
	})

	It("cSamplingParams matches vllm_sampling_params (ABI v2)", func() {
		var p cSamplingParams
		Expect(unsafe.Offsetof(p.Temperature)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(p.TopP)).To(Equal(uintptr(4)))
		Expect(unsafe.Offsetof(p.TopK)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(p.MinP)).To(Equal(uintptr(12)))
		Expect(unsafe.Offsetof(p.MaxTokens)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(p.Seed)).To(Equal(uintptr(24)))
		Expect(unsafe.Offsetof(p.HasSeed)).To(Equal(uintptr(32)))
		Expect(unsafe.Offsetof(p.PresencePenalty)).To(Equal(uintptr(36)))
		Expect(unsafe.Offsetof(p.FrequencyPenalty)).To(Equal(uintptr(40)))
		Expect(unsafe.Offsetof(p.RepetitionPenalty)).To(Equal(uintptr(44)))
		Expect(unsafe.Offsetof(p.MinTokens)).To(Equal(uintptr(48)))
		Expect(unsafe.Offsetof(p.IgnoreEOS)).To(Equal(uintptr(52)))
		Expect(unsafe.Offsetof(p.Stop)).To(Equal(uintptr(56)))
		Expect(unsafe.Offsetof(p.NStop)).To(Equal(uintptr(64)))
		Expect(unsafe.Offsetof(p.StructuredJSON)).To(Equal(uintptr(72)))
		Expect(unsafe.Offsetof(p.StructuredRegex)).To(Equal(uintptr(80)))
		Expect(unsafe.Offsetof(p.StructuredChoice)).To(Equal(uintptr(88)))
		Expect(unsafe.Offsetof(p.NStructuredChoice)).To(Equal(uintptr(96)))
		Expect(unsafe.Offsetof(p.StructuredGrammar)).To(Equal(uintptr(104)))
		Expect(unsafe.Offsetof(p.StructuredJSONObject)).To(Equal(uintptr(112)))
		Expect(unsafe.Sizeof(p)).To(Equal(uintptr(120)))
	})

	It("cCompletion matches vllm_completion", func() {
		var c cCompletion
		Expect(unsafe.Offsetof(c.Text)).To(Equal(uintptr(0)))
		Expect(unsafe.Offsetof(c.FinishReason)).To(Equal(uintptr(8)))
		Expect(unsafe.Offsetof(c.PromptTokens)).To(Equal(uintptr(16)))
		Expect(unsafe.Offsetof(c.CompletionTokens)).To(Equal(uintptr(20)))
		Expect(unsafe.Sizeof(c)).To(Equal(uintptr(24)))
	})
})

var _ = Describe("parseOptions", func() {
	It("extracts the engine sizing knobs", func() {
		lo := parseOptions(&pb.ModelOptions{Options: []string{
			"block_size:64", "num_blocks:512", "max_num_seqs:32", "unknown:ignored",
		}})
		Expect(lo.blockSize).To(Equal(int32(64)))
		Expect(lo.numBlocks).To(Equal(int32(512)))
		Expect(lo.maxNumSeqs).To(Equal(int32(32)))
	})
	It("ignores malformed and non-positive values", func() {
		lo := parseOptions(&pb.ModelOptions{Options: []string{
			"block_size:abc", "num_blocks:-1", "max_num_seqs", "block_size:0",
		}})
		Expect(lo).To(Equal(loadOptions{}))
	})
})

var _ = Describe("samplingFromPredict", func() {
	It("maps the sampling fields onto the C POD", func() {
		sp, _ := samplingFromPredict(&pb.PredictOptions{
			Temperature:      0.7,
			TopP:             0.9,
			TopK:             40,
			MinP:             0.05,
			Tokens:           128,
			Seed:             42,
			Penalty:          1.1,
			PresencePenalty:  0.5,
			FrequencyPenalty: 0.25,
			IgnoreEOS:        true,
		})
		Expect(sp.Temperature).To(BeNumerically("~", 0.7, 1e-6))
		Expect(sp.TopP).To(BeNumerically("~", 0.9, 1e-6))
		Expect(sp.TopK).To(Equal(int32(40)))
		Expect(sp.MinP).To(BeNumerically("~", 0.05, 1e-6))
		Expect(sp.MaxTokens).To(Equal(int32(128)))
		Expect(sp.HasSeed).To(Equal(int32(1)))
		Expect(sp.Seed).To(Equal(uint64(42)))
		Expect(sp.RepetitionPenalty).To(BeNumerically("~", 1.1, 1e-6))
		Expect(sp.PresencePenalty).To(BeNumerically("~", 0.5, 1e-6))
		Expect(sp.FrequencyPenalty).To(BeNumerically("~", 0.25, 1e-6))
		Expect(sp.IgnoreEOS).To(Equal(int32(1)))
	})

	It("keeps the engine defaults for unset fields and stays unseeded", func() {
		sp, keep := samplingFromPredict(&pb.PredictOptions{})
		Expect(sp.TopP).To(BeNumerically("~", 1.0, 1e-6))
		Expect(sp.RepetitionPenalty).To(BeNumerically("~", 1.0, 1e-6))
		Expect(sp.MaxTokens).To(Equal(int32(0))) // unbounded, engine-capped.
		Expect(sp.HasSeed).To(Equal(int32(0)))
		Expect(sp.Stop).To(Equal(uintptr(0)))
		Expect(sp.StructuredGrammar).To(Equal(uintptr(0)))
		Expect(keep).To(BeEmpty())
	})

	It("wires stop prompts and the grammar constraint", func() {
		sp, keep := samplingFromPredict(&pb.PredictOptions{
			StopPrompts: []string{"</s>", "\n\n"},
			Grammar:     "root ::= \"yes\" | \"no\"",
		})
		Expect(sp.NStop).To(Equal(int32(2)))
		Expect(sp.Stop).NotTo(Equal(uintptr(0)))
		Expect(sp.StructuredGrammar).NotTo(Equal(uintptr(0)))
		Expect(keep).NotTo(BeEmpty())
	})
})

var _ = Describe("validModelPath", func() {
	It("accepts a .gguf file", func() {
		dir := GinkgoT().TempDir()
		p := filepath.Join(dir, "model.gguf")
		Expect(os.WriteFile(p, []byte("GGUF"), 0o600)).To(Succeed())
		Expect(validModelPath(p)).To(Succeed())
	})
	It("accepts a directory with config.json", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600)).To(Succeed())
		Expect(validModelPath(dir)).To(Succeed())
	})
	It("refuses a directory without config.json (greedy-probe rule)", func() {
		Expect(validModelPath(GinkgoT().TempDir())).NotTo(Succeed())
	})
	It("refuses a non-gguf file", func() {
		dir := GinkgoT().TempDir()
		p := filepath.Join(dir, "weights.bin")
		Expect(os.WriteFile(p, []byte("x"), 0o600)).To(Succeed())
		Expect(validModelPath(p)).NotTo(Succeed())
	})
	It("refuses a missing path", func() {
		Expect(validModelPath("/nonexistent/model.gguf")).NotTo(Succeed())
	})
})
