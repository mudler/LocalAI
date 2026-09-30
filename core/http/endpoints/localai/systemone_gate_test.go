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

var _ = Describe("systemone routing by model kind", func() {
	mk := func(backend string, usecases ...string) config.ModelConfig {
		c := config.ModelConfig{Name: "m", Backend: backend}
		if len(usecases) > 0 {
			c.KnownUsecases = config.GetUsecasesFromYAML(usecases)
		}
		return c
	}

	Describe("systemOneUsesDecisionPipeline", func() {
		It("sends a declared decision model to the decision pipeline", func() {
			Expect(systemOneUsesDecisionPipeline(mk("vllm-cpp", "systemone"))).To(BeTrue())
		})
		It("sends a token_classify model to the NER path, since vllm_decide refuses NER architectures", func() {
			Expect(systemOneUsesDecisionPipeline(mk("vllm-cpp", "token_classify"))).To(BeFalse())
		})
		It("keeps configs that declare nothing on the decision pipeline", func() {
			Expect(systemOneUsesDecisionPipeline(mk("vllm-cpp"))).To(BeTrue())
		})
		It("prefers the decision pipeline when both usecases are declared", func() {
			Expect(systemOneUsesDecisionPipeline(mk("vllm-cpp", "systemone", "token_classify"))).To(BeTrue())
		})
		It("never uses it for a backend without the Score RPC", func() {
			Expect(systemOneUsesDecisionPipeline(mk("no-such-backend", "systemone"))).To(BeFalse())
		})
	})

	Describe("systemOneNERAllowed", func() {
		It("refuses a decision model on the NER-only routes with an actionable message", func() {
			Expect(systemOneNERAllowed(mk("vllm-cpp", "systemone"))).To(MatchError(ContainSubstring("/v1/systemone")))
		})
		It("accepts a token_classify model", func() {
			Expect(systemOneNERAllowed(mk("vllm-cpp", "token_classify"))).To(Succeed())
		})
		It("accepts configs that declare nothing", func() {
			Expect(systemOneNERAllowed(mk("vllm-cpp"))).To(Succeed())
		})
	})
})
