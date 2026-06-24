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
		req := newTTSRequest("hi", "/m", "alloy", "/out.wav", "en", "cheerful narrator", nil, "m")
		Expect(req.Instructions).ToNot(BeNil())
		Expect(req.GetInstructions()).To(Equal("cheerful narrator"))
		Expect(req.GetText()).To(Equal("hi"))
		Expect(req.GetVoice()).To(Equal("alloy"))
		Expect(req.GetDst()).To(Equal("/out.wav"))
		Expect(req.GetLanguage()).To(Equal("en"))
	})

	It("leaves instructions unset when empty so backends fall back to YAML", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil, "m")
		Expect(req.Instructions).To(BeNil())
		Expect(req.GetInstructions()).To(Equal(""))
	})

	It("forwards per-request params through to the backend", func() {
		params := map[string]string{"exaggeration": "0.7", "cfg_weight": "0.3"}
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", params, "m")
		Expect(req.GetParams()).To(HaveKeyWithValue("exaggeration", "0.7"))
		Expect(req.GetParams()).To(HaveKeyWithValue("cfg_weight", "0.3"))
	})

	It("leaves params nil when none are supplied", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil, "m")
		Expect(req.GetParams()).To(BeNil())
	})
})

// TTSRequest carries TWO model strings and they are not interchangeable.
// `Model` is a path that FileStagingClient.TTS rewrites into a worker-local
// absolute path in distributed mode; `ModelIdentity` is the untranslated
// ModelConfig.Model the backend compares against what it loaded (#10952).
// Comparing `Model` instead would reject valid requests in exactly the
// configuration this guards, so these specs pin them as separate inputs.
var _ = Describe("newTTSRequest model identity", func() {
	It("carries the untranslated identity alongside the staged model path", func() {
		req := newTTSRequest("hi", "/worker/local/staged.onnx", "", "/out.wav", "", "", nil, "voices/piper-en.onnx")
		Expect(req.GetModelIdentity()).To(Equal("voices/piper-en.onnx"))
		Expect(req.GetModel()).To(Equal("/worker/local/staged.onnx"))
	})

	It("keeps the identity empty when the config names no model, so the backend skips", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil, "")
		Expect(req.GetModelIdentity()).To(BeEmpty())
	})
})
