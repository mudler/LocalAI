package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("familyFor", func() {
	It("maps the NeMo architectures to their families", func() {
		for arch, want := range map[string]family{
			"asr":        familyASR,
			"sortformer": familyDiarization,
			"magpietts":  familyTTS,
		} {
			got, err := familyFor(arch)
			Expect(err).ToNot(HaveOccurred(), "arch %q", arch)
			Expect(got).To(Equal(want), "arch %q", arch)
		}
	})

	It("treats an unknown architecture as NMT", func() {
		// NMT GGUFs are produced by llama.cpp's converter, so they carry an LLM
		// architecture such as qwen3 rather than a NeMo-specific string.
		got, err := familyFor("qwen3")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(familyNMT))
	})

	It("rejects an auxiliary-only architecture as a primary model", func() {
		for _, arch := range []string{"nemo-nano-codec", "vad", "pnc"} {
			_, err := familyFor(arch)
			Expect(err).To(HaveOccurred(), "arch %q", arch)
			Expect(err.Error()).To(ContainSubstring(arch))
		}
	})
})

var _ = Describe("ggufArchitecture", func() {
	It("returns an error rather than panicking on a file that is not a GGUF", func() {
		p := filepath.Join(GinkgoT().TempDir(), "not-a-model.gguf")
		Expect(os.WriteFile(p, []byte("definitely not a gguf header"), 0o600)).To(Succeed())

		_, err := ggufArchitecture(p)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(p))
	})
})

var _ = Describe("discoverTTSAssets", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	write := func(name string) string {
		p := filepath.Join(dir, name)
		Expect(os.WriteFile(p, []byte("x"), 0o600)).To(Succeed())
		return p
	}

	It("finds a sibling nanocodec gguf and extracted dir", func() {
		primary := write("magpie.f16.gguf")
		codec := write("nemo-nano-codec-22khz.f16.gguf")
		Expect(os.Mkdir(filepath.Join(dir, "extracted"), 0o755)).To(Succeed())

		o := loadOptions{}
		Expect(discoverTTSAssets(primary, &o)).To(Succeed())
		Expect(o.codecModel).To(Equal(codec))
		Expect(o.tokenizerDir).To(Equal(filepath.Join(dir, "extracted")))
	})

	It("never selects the primary gguf as its own codec", func() {
		// A file named so it would match a naive *.gguf scan.
		primary := write("nanocodec-magpie.gguf")
		Expect(os.Mkdir(filepath.Join(dir, "extracted"), 0o755)).To(Succeed())

		o := loadOptions{}
		err := discoverTTSAssets(primary, &o)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("codec_model"))
	})

	It("does not overwrite explicitly configured paths", func() {
		primary := write("magpie.f16.gguf")
		write("nemo-nano-codec.gguf")
		Expect(os.Mkdir(filepath.Join(dir, "extracted"), 0o755)).To(Succeed())

		o := loadOptions{codecModel: "/explicit/codec.gguf", tokenizerDir: "/explicit/tok"}
		Expect(discoverTTSAssets(primary, &o)).To(Succeed())
		Expect(o.codecModel).To(Equal("/explicit/codec.gguf"))
		Expect(o.tokenizerDir).To(Equal("/explicit/tok"))
	})

	It("names the missing option key when the tokenizer dir cannot be found", func() {
		primary := write("magpie.f16.gguf")
		write("nemo-nano-codec.gguf")

		o := loadOptions{}
		err := discoverTTSAssets(primary, &o)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tokenizer_dir"))
	})
})
