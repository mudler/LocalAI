package config

import (
	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A current architecture can carry metadata in a shape the pinned parser does
// not expect. The nemotron_h_moe files, for one, store expert_used_count as a
// per-layer array, and the parser panics reading it as a number. That must not
// cost the model the rest of its defaults.
var _ = Describe("Reading the header of a file the parser cannot decode", func() {
	unreadable := func() *gguf.GGUFFile {
		f := ggufShape{arch: "nemotron_h_moe", layers: 4, embd: 1024, heads: 8, kvHeads: 2, headDim: 128, ffn: 2048}.file()
		f.Header.MetadataKV = append(f.Header.MetadataKV,
			ggufKVString("tokenizer.chat_template", "{{ messages }}"),
			gguf.GGUFMetadataKV{
				Key: "nemotron_h_moe.expert_used_count", ValueType: gguf.GGUFMetadataValueTypeArray,
				Value: gguf.GGUFMetadataKVArrayValue{Type: gguf.GGUFMetadataValueTypeInt32, Len: 2, Array: []any{int32(6), int32(6)}},
			})
		return f
	}

	It("is a file the parser really cannot decode", func() {
		_, ok := safeArchitecture(unreadable())
		Expect(ok).To(BeFalse())
	})

	It("keeps the template and the architecture name, with the context unknown", func() {
		var h ggufHeader
		Expect(func() { h = readGGUFHeader(unreadable()) }).NotTo(Panic())
		Expect(h.Architecture).To(Equal("nemotron_h_moe"))
		Expect(h.HasChatTemplate).To(BeTrue())
		Expect(h.ChatTemplate).To(Equal("{{ messages }}"))
		Expect(h.TrainedContext).To(BeZero())
		Expect(h.trainedPanic).To(BeNil())
		Expect(h.ramFootprintAt(32768)).To(BeZero())
	})
})

// A header whose attention geometry is missing gives an estimate with no KV
// cache. Its footprint then barely changes with the window, so the fit checks
// would see no reason to lower it. The header must report it as unknown.
var _ = Describe("Reading the header of a file with no usable KV", func() {
	const gib = uint64(1) << 30

	var origVRAM, origRAM func() uint64
	BeforeEach(func() {
		origVRAM, origRAM = perDeviceVRAM, systemTotalRAM
		perDeviceVRAM = func() uint64 { return 0 }
		systemTotalRAM = func() uint64 { return 16 * gib }
	})
	AfterEach(func() { perDeviceVRAM, systemTotalRAM = origVRAM, origRAM })

	shape := func(kvHeads uint32) ggufShape {
		return ggufShape{arch: "llama", layers: 64, embd: 5120, heads: 40, kvHeads: kvHeads, headDim: 128, ffn: 25600}
	}

	It("keeps the footprints of a header with KV", func() {
		h := readGGUFHeader(shape(8).file())
		Expect(h.TrainedContext).To(BeNumerically(">", 0))
		Expect(h.ramFootprintAt(32768)).To(BeNumerically(">", h.ramFootprintAt(4096)))
	})

	It("treats a header with zero kv heads as unknown", func() {
		h := readGGUFHeader(shape(0).file())
		Expect(h.TrainedContext).To(BeNumerically(">", 0))
		for _, ctx := range []int{4096, 16384, 32768} {
			Expect(h.footprintAt(ctx)).To(BeZero())
			Expect(h.ramFootprintAt(ctx)).To(BeZero())
		}
		Expect(h.maxCtxFootprint).To(BeZero())
	})

	It("leaves the base cap alone when the footprint is unknown", func() {
		h := readGGUFHeader(shape(0).file())
		// A model that needs about 25 GiB of 16 GiB would be stepped down if the
		// estimate were believed; unknown means no step-down, the base cap holds.
		Expect(autoContextSize(h.footprintAt, h.ramFootprintAt, 40960, 32768)).To(Equal(32768))
	})
})
