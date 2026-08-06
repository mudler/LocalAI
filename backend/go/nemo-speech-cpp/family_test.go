package main

import (
	"encoding/binary"
	"os"
	"path/filepath"

	gguf "github.com/gpustack/gguf-parser-go"
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

// ggufWithArchValue builds a minimal GGUF v3 carrying general.architecture as
// its single metadata entry, with the caller's value type and encoded value.
func ggufWithArchValue(valueType gguf.GGUFMetadataValueType, value []byte) []byte {
	const key = "general.architecture"

	var b []byte
	b = append(b, 'G', 'G', 'U', 'F')
	b = binary.LittleEndian.AppendUint32(b, 3) // version
	b = binary.LittleEndian.AppendUint64(b, 0) // tensor count
	b = binary.LittleEndian.AppendUint64(b, 1) // metadata kv count
	b = binary.LittleEndian.AppendUint64(b, uint64(len(key)))
	b = append(b, key...)
	b = binary.LittleEndian.AppendUint32(b, uint32(valueType))
	return append(b, value...)
}

// writeGGUFWithUint32Arch writes a minimal GGUF v3 whose single metadata entry
// is general.architecture typed UINT32 rather than STRING. Handwritten and
// half-converted files really do carry mistyped keys, and the parser hands them
// back rather than rejecting them.
func writeGGUFWithUint32Arch(path string) {
	b := ggufWithArchValue(gguf.GGUFMetadataValueTypeUint32, binary.LittleEndian.AppendUint32(nil, 7))
	ExpectWithOffset(1, os.WriteFile(path, b, 0o600)).To(Succeed())
}

// writeGGUFWithArch writes a minimal GGUF v3 that parses cleanly and reports
// arch as its general.architecture. It is the only way to reach the code past
// ggufArchitecture in a test, since there are no real NeMo GGUFs to point at.
func writeGGUFWithArch(path, arch string) {
	v := binary.LittleEndian.AppendUint64(nil, uint64(len(arch)))
	v = append(v, arch...)
	ExpectWithOffset(1, os.WriteFile(path, ggufWithArchValue(gguf.GGUFMetadataValueTypeString, v), 0o600)).To(Succeed())
}

var _ = Describe("ggufArchitecture", func() {
	It("returns an error rather than panicking on a file that is not a GGUF", func() {
		p := filepath.Join(GinkgoT().TempDir(), "not-a-model.gguf")
		Expect(os.WriteFile(p, []byte("definitely not a gguf header"), 0o600)).To(Succeed())

		_, err := ggufArchitecture(p)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(p))
	})

	It("returns an error rather than panicking when general.architecture is not a string", func() {
		p := filepath.Join(GinkgoT().TempDir(), "mistyped-arch.gguf")
		writeGGUFWithUint32Arch(p)

		arch, err := ggufArchitecture(p)
		Expect(err).To(HaveOccurred())
		Expect(arch).To(BeEmpty())
		Expect(err.Error()).To(ContainSubstring("general.architecture"))
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

	It("never selects the primary gguf as its own codec through an uncleaned path", func() {
		// LocalAI joins the model directory and the model name itself, so a
		// trailing separator on ModelPath produces a doubled slash here. The
		// self-codec guard has to survive that.
		write("nanocodec-magpie.gguf")
		primary := dir + "//nanocodec-magpie.gguf"
		Expect(os.Mkdir(filepath.Join(dir, "extracted"), 0o755)).To(Succeed())

		o := loadOptions{}
		err := discoverTTSAssets(primary, &o)
		Expect(err).To(HaveOccurred())
		Expect(o.codecModel).To(BeEmpty())
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
