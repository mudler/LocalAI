package backend

// Specs for the TTSRequest assembly that carries the per-request
// instructions/params from the OpenAI `instructions` field (and the LocalAI
// `params` extension) through to the gRPC boundary. Before this plumbing the
// instruction value was dropped before reaching the backend; these specs pin
// that it now survives, and that the empty case stays backward compatible.

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("newTTSRequest", func() {
	It("attaches the instructions when a per-request value is set", func() {
		req := newTTSRequest("hi", "/m", "alloy", "/out.wav", "en", "cheerful narrator", nil)
		Expect(req.Instructions).ToNot(BeNil())
		Expect(req.GetInstructions()).To(Equal("cheerful narrator"))
		Expect(req.GetText()).To(Equal("hi"))
		Expect(req.GetVoice()).To(Equal("alloy"))
		Expect(req.GetDst()).To(Equal("/out.wav"))
		Expect(req.GetLanguage()).To(Equal("en"))
	})

	It("leaves instructions unset when empty so backends fall back to YAML", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil)
		Expect(req.Instructions).To(BeNil())
		Expect(req.GetInstructions()).To(Equal(""))
	})

	It("forwards per-request params through to the backend", func() {
		params := map[string]string{"exaggeration": "0.7", "cfg_weight": "0.3"}
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", params)
		Expect(req.GetParams()).To(HaveKeyWithValue("exaggeration", "0.7"))
		Expect(req.GetParams()).To(HaveKeyWithValue("cfg_weight", "0.3"))
	})

	It("leaves params nil when none are supplied", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil)
		Expect(req.GetParams()).To(BeNil())
	})
})
