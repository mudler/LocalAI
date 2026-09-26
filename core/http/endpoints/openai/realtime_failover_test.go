package openai

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/failover"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type rtSource map[string]config.ModelConfig

func (s rtSource) GetModelConfig(n string) (config.ModelConfig, bool) { c, ok := s[n]; return c, ok }
func (s rtSource) GetAllModelsConfigs() []config.ModelConfig {
	var out []config.ModelConfig
	for _, c := range s {
		out = append(out, c)
	}
	return out
}

var _ = Describe("realtime failover", func() {
	var fm *failover.Manager

	BeforeEach(func() {
		fm = failover.New(rtSource{
			"a":     {Name: "a", Backend: "cloud-proxy"},
			"b":     {Name: "b", Backend: "llama-cpp"},
			"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{{Model: "a"}, {Model: "b"}}}},
		})
	})

	chainModel := func() *wrappedModel {
		return &wrappedModel{failover: fm, stageChains: map[string]string{"tts": "chain"},
			stageTargetConfig: func(name string) (*config.ModelConfig, error) { return &config.ModelConfig{Name: name}, nil }}
	}

	It("routes a chain stage through the plan and retries before commit", func() {
		m := chainModel()
		var tried []string
		err := m.stageCall(context.Background(), "tts", nil, func(cfg *config.ModelConfig, _ func()) error {
			tried = append(tried, cfg.Name)
			if cfg.Name == "a" {
				return errors.New("dial tcp: refused")
			}
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(tried).To(Equal([]string{"a", "b"}))
	})

	It("does not retry a chain stage once output was committed", func() {
		m := chainModel()
		var tried []string
		err := m.stageCall(context.Background(), "tts", nil, func(cfg *config.ModelConfig, commit func()) error {
			tried = append(tried, cfg.Name)
			commit()
			return errors.New("dial tcp: refused")
		})
		Expect(err).To(HaveOccurred())
		Expect(tried).To(Equal([]string{"a"}))
	})

	It("calls a plain stage once with its own config", func() {
		m := &wrappedModel{}
		base := &config.ModelConfig{Name: "plain"}
		calls := 0
		err := m.stageCall(context.Background(), "tts", base, func(cfg *config.ModelConfig, _ func()) error {
			calls++
			Expect(cfg).To(BeIdenticalTo(base))
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(calls).To(Equal(1))
	})

	It("sends initial events, then switch events, and stops on cancel", func() {
		t := &fakeTransport{}
		failoverEvents := func() []types.ModelFailoverEvent {
			var out []types.ModelFailoverEvent
			for _, e := range t.events() {
				if fe, ok := e.(types.ModelFailoverEvent); ok {
					out = append(out, fe)
				}
			}
			return out
		}
		stop := startFailoverEvents(t, fm, map[string]string{"llm": "chain"})
		Eventually(failoverEvents).Should(ContainElement(And(
			HaveField("Reason", "initial"), HaveField("To", "a"), HaveField("Stage", "llm"))))
		fm.ReportFailure("a", errors.New("dial tcp: refused"))
		Eventually(failoverEvents).Should(ContainElement(And(
			HaveField("Reason", "trip"), HaveField("From", "a"), HaveField("To", "b"), HaveField("Chain", "chain"))))
		stop()
	})
})
