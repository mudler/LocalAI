// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type nativeRunner struct{}

func (nativeRunner) Decide(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	v := .8
	return &schema.SystemOneResponse{Answers: map[string]schema.SystemOneAnswer{"p0": {Type: "noul", Noul: &v}}}, nil
}

var _ = Describe("decisions central factory", func() {
	It("requires explicit usecase and invalidates on native model edits", func() {
		cfg := &config.ModelConfig{Name: "router", Router: config.RouterConfig{Classifier: "decisions", ClassifierModel: "native", Policies: []config.RouterPolicy{{Label: "code", Description: "coding"}}, Candidates: []config.RouterCandidate{{Model: "target", Labels: []string{"code"}}}}}
		flags := config.FLAG_DECISIONS
		model := &config.ModelConfig{Name: "native", Backend: "vllm-cpp", KnownUsecases: &flags}
		deps := ClassifierDeps{Decisions: func(string) backend.DecisionRunner { return nativeRunner{} }, ModelLookup: func(string) *config.ModelConfig { return model }}
		registry := router.NewRegistry()
		first, err := GetOrBuildClassifier(registry, cfg, deps)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Name()).To(Equal("decisions"))
		same, err := GetOrBuildClassifier(registry, cfg, deps)
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(BeIdenticalTo(first))
		model.Model = "new-weights.gguf"
		changed, err := GetOrBuildClassifier(registry, cfg, deps)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).NotTo(BeIdenticalTo(first))
		Expect(model.StampPersistedConfigRevision()).To(Succeed())
		revised, err := GetOrBuildClassifier(registry, cfg, deps)
		Expect(err).NotTo(HaveOccurred())
		Expect(revised).NotTo(BeIdenticalTo(changed))
		model.KnownUsecases = nil
		_, err = GetOrBuildClassifier(registry, cfg, deps)
		Expect(err).To(HaveOccurred())
		deps.ModelLookup = nil
		_, err = GetOrBuildClassifier(router.NewRegistry(), cfg, deps)
		Expect(err).To(HaveOccurred())
	})
})
