package localai

import (
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("systemOneModelAllowed", func() {
	mk := func(usecases ...string) config.ModelConfig {
		return config.ModelConfig{
			Name:          "m",
			Backend:       "vllm-cpp",
			KnownUsecases: config.GetUsecasesFromYAML(usecases),
		}
	}

	It("accepts a declared systemone model", func() {
		Expect(systemOneModelAllowed(mk("systemone"))).To(Succeed())
	})

	It("accepts a token_classify model, which the NER path serves", func() {
		Expect(systemOneModelAllowed(mk("token_classify"))).To(Succeed())
	})

	It("keeps configs that declare no usecases working", func() {
		Expect(systemOneModelAllowed(config.ModelConfig{Name: "laya", Backend: "vllm-cpp"})).To(Succeed())
	})

	It("refuses a chat-only model with an actionable message", func() {
		Expect(systemOneModelAllowed(mk("chat"))).To(MatchError(ContainSubstring("known_usecases: [systemone]")))
	})
})
