package agents

import (
	"encoding/json"

	"github.com/mudler/LocalAGI/core/state"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("knowledge-base models", func() {
	It("preserves optional models through native and LocalAGI config JSON", func() {
		payload := []byte(`{"name":"Research","embedding_model":"embed-custom","reranker_model":"rank-custom"}`)
		var native AgentConfig
		Expect(json.Unmarshal(payload, &native)).To(Succeed())
		data, err := json.Marshal(native)
		Expect(err).NotTo(HaveOccurred())
		var embedded state.AgentConfig
		Expect(json.Unmarshal(data, &embedded)).To(Succeed())
		Expect(embedded.EmbeddingModel).To(Equal("embed-custom"))
		Expect(embedded.RerankerModel).To(Equal("rank-custom"))
	})
	It("exposes both optional selectors in ModelSettings", func() {
		fields := map[string]ConfigField{}
		for _, field := range DefaultConfigMeta().Fields {
			fields[field.Name] = field
		}
		for _, name := range []string{"embedding_model", "reranker_model"} {
			field, ok := fields[name]
			Expect(ok).To(BeTrue())
			Expect(field.Required).To(BeFalse())
			Expect(field.Tags.Section).To(Equal("ModelSettings"))
		}
	})
})
