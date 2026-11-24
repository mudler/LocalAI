package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("quantization markers", func() {
	DescribeTable("NameStem strips the markers that distinguish builds, not models",
		func(name, expected string) {
			Expect(NameStem(name)).To(Equal(expected))
		},
		Entry("plain q4", "foo-model-q4_k_m", "foo-model"),
		Entry("q8_0", "foo-model-q8_0", "foo-model"),
		Entry("q5_1", "foo-model-q5_1", "foo-model"),
		Entry("q2 with group size", "ternary-bonsai-8b-q2-g64", "ternary-bonsai-8b"),
		Entry("iq variant", "ideogram-4-iq4nl-ggml", "ideogram-4"),
		Entry("i1 imatrix", "orca-agent-v0.1-i1", "orca-agent-v0.1"),
		Entry("f16", "ced-base-f16", "ced-base"),
		Entry("bf16", "some-model-bf16", "some-model"),
		Entry("fp8", "some-model-fp8", "some-model"),
		Entry("nvfp4", "qwen3.6-27b-nvfp4", "qwen3.6-27b"),
		Entry("mxfp4_moe", "huihui-qwen3-vl-30b-a3b-instruct-abliterated-mxfp4_moe", "huihui-qwen3-vl-30b-a3b-instruct-abliterated"),
		Entry("pq2", "ternary-bonsai-8b-pq2", "ternary-bonsai-8b"),
		Entry("awq", "some-model-awq", "some-model"),
		Entry("gptq", "some-model-gptq", "some-model"),
		Entry("Nbit", "qwen3-8b-mlx-4bit", "qwen3-8b-mlx"),
		Entry("gguf", "some-model-gguf", "some-model"),
		Entry("ggml", "flux.1-dev-ggml", "flux.1-dev"),
		Entry("qat is a quantization technique", "gemma-3-27b-it-qat", "gemma-3-27b-it"),
		Entry("apex is a quantization technique", "qwen3.6-35b-a3b-apex", "qwen3.6-35b-a3b"),
		Entry("stacked markers", "gemma-4-e2b-it-qat-q4_0", "gemma-4-e2b-it"),
		Entry("the config suffix is dropped", "phi-2-chat:Q8_0", "phi-2-chat"),
		Entry("a non-quant config suffix is dropped too", "meta-llama-3.1-8b-instruct:grammar-functioncall", "meta-llama-3.1-8b-instruct"),
	)

	DescribeTable("NameStem leaves alone what identifies a different model",
		func(name, expected string) {
			Expect(NameStem(name)).To(Equal(expected))
		},
		Entry("parameter size", "qwen3-tts-cpp-0.6b-base", "qwen3-tts-cpp-0.6b-base"),
		Entry("language suffix", "kokoros-de", "kokoros-de"),
		Entry("English-only ASR", "whisper-small-en", "whisper-small-en"),
		Entry("finetune", "qwen3-30b-a3b-abliterated", "qwen3-30b-a3b-abliterated"),
		Entry("product suffix", "vibevoice-cpp-asr", "vibevoice-cpp-asr"),
	)

	It("never strips a name down to nothing", func() {
		Expect(NameStem("q4_k_m")).To(Equal("q4_k_m"))
		Expect(NameStem("f16-q8_0")).To(Equal("f16"))
	})

	DescribeTable("FileStem reduces a weight filename to the weights it holds",
		func(filename, expected string) {
			Expect(FileStem(filename)).To(Equal(expected))
		},
		Entry("directory and extension go", "bonsai/models/Ternary-Bonsai-8B-gguf/Ternary-Bonsai-8B-Q2_0.gguf", "ternary-bonsai-8b"),
		Entry("underscored quant token stays whole", "Llama-3.2-1B-Instruct-Q4_K_M.gguf", "llama-3.2-1b-instruct"),
		Entry("dot separated quant token", "Llama-3.2-3B-Instruct.Q4_K_M.gguf", "llama-3.2-3b-instruct"),
		Entry("group size suffix", "Ternary-Bonsai-8B-Q2_0_g64.gguf", "ternary-bonsai-8b"),
		Entry("bf16", "omnivoice-cpp-hq/omnivoice-base-BF16.gguf", "omnivoice-base"),
		Entry("safetensors", "some/dir/Model-Name-fp8.safetensors", "model-name"),
	)

	DescribeTable("BuildWidth reads the nominal width out of a filename",
		func(filename string, expected int) {
			Expect(BuildWidth(filename)).To(Equal(expected))
		},
		Entry("q4", "foo-Q4_K_M.gguf", 4),
		Entry("q8", "foo-Q8_0.gguf", 8),
		Entry("q2", "foo-Q2_0.gguf", 2),
		Entry("f16", "foo-f16.gguf", 16),
		Entry("bf16", "foo-BF16.gguf", 16),
		Entry("iq3", "foo-iq3_xxs.gguf", 3),
		Entry("nothing readable sorts last", "foo.gguf", unknownWidth),
	)

	It("treats an auxiliary file as never being the model's own weights", func() {
		for _, f := range []string{
			"mmproj-model-f16.gguf",
			"dir/vae-BF16.gguf",
			"clip_l.safetensors",
			"umt5-xxl-encoder-Q8_0.gguf",
			"t5xxl_fp16.safetensors",
			"ae.safetensors",
			"omnivoice-tokenizer-Q8_0.gguf",
		} {
			Expect(IsAuxiliaryFile(f)).To(BeTrue(), "expected %q to be auxiliary", f)
		}
		Expect(IsAuxiliaryFile("gemma-3-27b-it-Q4_K_M.gguf")).To(BeFalse())
	})
})
