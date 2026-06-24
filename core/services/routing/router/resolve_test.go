package router_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/router"
)

type fakeClassifier struct {
	name     string
	decision router.Decision
	err      error
}

func (f *fakeClassifier) Classify(_ context.Context, _ router.Probe) (router.Decision, error) {
	if f.err != nil {
		return router.Decision{}, f.err
	}
	return f.decision, nil
}

func (f *fakeClassifier) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}

// loaderFrom returns a CandidateLoader serving cfgs by name. Missing
// entries return ("not found"). Keeps test setup compact — each spec
// declares the model name → config map it cares about.
func loaderFrom(cfgs map[string]*config.ModelConfig) router.CandidateLoader {
	return func(name string) (*config.ModelConfig, error) {
		c, ok := cfgs[name]
		if !ok {
			return nil, errors.New("not found: " + name)
		}
		return c, nil
	}
}

var _ = Describe("router.Resolve", func() {
	var (
		routerCfg *config.ModelConfig
		fast      *config.ModelConfig
		smart     *config.ModelConfig
		fallback  *config.ModelConfig
		loader    router.CandidateLoader
	)

	BeforeEach(func() {
		fast = &config.ModelConfig{Name: "fast-local", Backend: "llama-cpp"}
		smart = &config.ModelConfig{Name: "smart-cloud", Backend: "cloud-proxy"}
		fallback = &config.ModelConfig{Name: "fallback-local", Backend: "llama-cpp"}
		routerCfg = &config.ModelConfig{
			Name: "router-llm",
			Router: config.RouterConfig{
				Classifier: router.ClassifierScore,
				Candidates: []config.RouterCandidate{
					{Model: "fast-local", Labels: []string{"chat"}},
					{Model: "smart-cloud", Labels: []string{"reasoning"}},
				},
				Fallback: "fallback-local",
			},
		}
		loader = loaderFrom(map[string]*config.ModelConfig{
			"fast-local":     fast,
			"smart-cloud":    smart,
			"fallback-local": fallback,
		})
	})

	It("picks the candidate that covers the classifier's labels", func() {
		cls := &fakeClassifier{decision: router.Decision{Labels: []string{"reasoning"}, Score: 0.92, Latency: 5 * time.Millisecond}}
		got, err := router.Resolve(context.Background(), routerCfg, cls, loader, router.Probe{Prompt: "tricky"})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.ChosenModel).To(Equal("smart-cloud"))
		Expect(got.ChosenConfig).To(Equal(smart))
		Expect(got.UsedFallback).To(BeFalse())
		Expect(got.Labels).To(Equal([]string{"reasoning"}))
	})

	It("falls back when the classifier errors", func() {
		cls := &fakeClassifier{err: errors.New("boom")}
		got, err := router.Resolve(context.Background(), routerCfg, cls, loader, router.Probe{Prompt: "anything"})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.UsedFallback).To(BeTrue())
		Expect(got.ChosenModel).To(Equal("fallback-local"))
		Expect(got.Labels).To(Equal([]string{router.LabelFallback}))
	})

	It("falls back when no candidate covers the active labels", func() {
		cls := &fakeClassifier{decision: router.Decision{Labels: []string{"unknown-label"}}}
		got, err := router.Resolve(context.Background(), routerCfg, cls, loader, router.Probe{Prompt: "x"})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.UsedFallback).To(BeTrue())
		Expect(got.ChosenModel).To(Equal("fallback-local"))
	})

	It("falls back when classifier is nil (build failed upstream)", func() {
		got, err := router.Resolve(context.Background(), routerCfg, nil, loader, router.Probe{Prompt: "x"})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.UsedFallback).To(BeTrue())
		Expect(got.ChosenModel).To(Equal("fallback-local"))
	})

	It("returns a terminal error when classifier fails AND no fallback is configured", func() {
		routerCfg.Router.Fallback = ""
		_, err := router.Resolve(context.Background(), routerCfg, nil, loader, router.Probe{Prompt: "x"})
		Expect(err).To(HaveOccurred())
	})

	It("rejects candidates that are themselves routers (depth-1 invariant)", func() {
		// Swap the fast-local config for one that itself has a router
		// block — the depth-1 guard must reject it.
		fast.Router = config.RouterConfig{
			Candidates: []config.RouterCandidate{{Model: "deeper", Labels: []string{"x"}}},
		}
		cls := &fakeClassifier{decision: router.Decision{Labels: []string{"chat"}}}
		_, err := router.Resolve(context.Background(), routerCfg, cls, loader, router.Probe{Prompt: "x"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("depth-1 invariant"))
	})
})
