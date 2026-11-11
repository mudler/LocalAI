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
