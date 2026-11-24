package schema_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SystemOne input preservation", func() {
	It("preserves structured input and absent, null, or empty images semantically", func() {
		for _, input := range []string{
			`{"state":{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]},"questions":{"q":{"type":"choice","instructions":{"text":"choose"},"criteria":{"yes":null,"no":"negative"}}},"images":["data:image/png;base64,AA=="]}`,
			`{"state":"text","questions":{},"images":[]}`,
			`{"state":"text","questions":{},"images":null}`,
			`{"state":"text","questions":{}}`,
		} {
			var req schema.SystemOneRequest
			Expect(json.Unmarshal([]byte(input), &req)).To(Succeed())
			output, err := json.Marshal(req)
			Expect(err).NotTo(HaveOccurred())
			var want, got any
			Expect(json.Unmarshal([]byte(input), &want)).To(Succeed())
			Expect(json.Unmarshal(output, &got)).To(Succeed())
			Expect(got).To(Equal(want))
		}
	})
	It("preserves images in permute requests semantically", func() {
		input := `{"request":{"state":"text","questions":{},"images":[{"url":"data:image/png;base64,AA=="}]},"question":"q"}`
		var req schema.SystemOnePermuteRequest
		Expect(json.Unmarshal([]byte(input), &req)).To(Succeed())
		output, err := json.Marshal(req)
		Expect(err).NotTo(HaveOccurred())
		var want, got any
		Expect(json.Unmarshal([]byte(input), &want)).To(Succeed())
		Expect(json.Unmarshal(output, &got)).To(Succeed())
		Expect(got).To(Equal(want))
	})
})
