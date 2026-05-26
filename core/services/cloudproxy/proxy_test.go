package cloudproxy

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rewriteModel", func() {
	It("is a no-op when upstream model is empty", func() {
		body := []byte(`{"model":"x","stream":false}`)
		out, err := rewriteModel(body, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(Equal(string(body)))
	})

	It("replaces the model", func() {
		body := []byte(`{"model":"alias","stream":false}`)
		out, err := rewriteModel(body, "real-model-id")
		Expect(err).NotTo(HaveOccurred())
		var m map[string]any
		Expect(json.Unmarshal(out, &m)).To(Succeed())
		Expect(m["model"]).To(Equal("real-model-id"))
	})
})

var _ = Describe("streaming", func() {
	It("detects stream=true", func() {
		Expect(streaming([]byte(`{"stream":true}`))).To(BeTrue())
	})
	It("detects stream=false", func() {
		Expect(streaming([]byte(`{"stream":false}`))).To(BeFalse())
	})
	It("returns false when stream key absent", func() {
		Expect(streaming([]byte(`{}`))).To(BeFalse())
	})
})
