package schema_test

import (
	"encoding/json"

	. "github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OpenAIRequest JSON unmarshaling", func() {
	It("accepts the embeddings dimensions parameter", func() {
		body := []byte(`{"model":"m","input":"hello","dimensions":128}`)

		var req OpenAIRequest
		Expect(json.Unmarshal(body, &req)).To(Succeed())

		Expect(req.Model).To(Equal("m"))
		Expect(req.Input).To(Equal("hello"))
		Expect(req.Dimensions).To(Equal(128))
	})
})
