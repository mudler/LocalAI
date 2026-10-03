// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("native decisions integration", Label("real-models"), func() {
	It("routes overlapping labels through the central factory and native runner", func() {
		weights, addr := os.Getenv("LOCALAI_DECISIONS_TEST_MODEL"), os.Getenv("LOCALAI_DECISIONS_TEST_GRPC")
		if weights == "" || addr == "" {
			Skip("requires an existing decision GGUF and a dedicated llama-cpp gRPC server")
		}
		abs, err := filepath.Abs(weights)
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(abs)
		Expect(err).NotTo(HaveOccurred())
		state := &system.SystemState{Model: system.Model{ModelsPath: filepath.Dir(abs)}}
		loader := model.NewModelLoader(state)
		app := config.NewApplicationConfig(config.WithSystemState(state), config.WithExternalBackend("llama-cpp", addr))
		flags, size, threads := config.FLAG_DECISIONS, 2048, 2
		native := &config.ModelConfig{Name: "native-integration", Backend: "llama-cpp", KnownUsecases: &flags}
		native.ContextSize = &size
		native.Model = filepath.Base(abs)
		native.Threads = &threads
		lookup := func(name string) *config.ModelConfig {
			if name == native.Name {
				return native
			}
			return nil
		}
		runner := backend.NewDecisionRunner(native.Name, lookup, loader, app)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// This separately checks the actual request/usage contract, not just labels.
		response, err := runner.Decide(ctx, &schema.SystemOneRequest{State: json.RawMessage(`"I was charged twice and need a refund today."`), Questions: map[string]schema.SystemOneQuestion{"refund": {Type: "noul", Instructions: json.RawMessage(`"Does the user request a refund?"`), Criteria: json.RawMessage(`{"false":"No refund requested","true":"Refund requested"}`)}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Answers).To(HaveKey("refund"))
		Expect(response.Answers["refund"].Noul).NotTo(BeNil())
		Expect(response.Usage.InputTokens).To(BeNumerically(">", 0))
		Expect(response.Usage.OutputTokens).To(Equal(0))
		raw, err := json.Marshal(response)
		Expect(err).NotTo(HaveOccurred())
		GinkgoWriter.Printf("native contract: %s\n", raw)
		// A deliberately low positive threshold checks overlapping-label plumbing,
		// not model quality. Both policies are independent binary questions.
		cfg := &config.ModelConfig{Name: "integration-router", Router: config.RouterConfig{Classifier: "decisions", ClassifierModel: native.Name, ActivationThreshold: 0.000001, Policies: []config.RouterPolicy{{Label: "billing", Description: "The user discusses a charge or payment."}, {Label: "refund", Description: "The user requests a refund."}}, Candidates: []config.RouterCandidate{{Model: "billing-only", Labels: []string{"billing"}}, {Model: "combined-target", Labels: []string{"billing", "refund"}}}}}
		classifier, err := GetOrBuildClassifier(router.NewRegistry(), cfg, ClassifierDeps{ModelLookup: lookup, Decisions: func(string) backend.DecisionRunner { return runner }})
		Expect(err).NotTo(HaveOccurred())
		result, err := router.Resolve(ctx, cfg, classifier, func(name string) (*config.ModelConfig, error) { return &config.ModelConfig{Name: name}, nil }, router.Probe{Prompt: "I was charged twice and need a refund today."})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.UsedFallback).To(BeFalse())
		Expect(result.Labels).To(ConsistOf("billing", "refund"))
		Expect(result.Decision.LabelScores).To(HaveLen(2))
		for _, score := range result.Decision.LabelScores {
			Expect(score.Score).To(BeNumerically(">=", 0))
			Expect(score.Score).To(BeNumerically("<=", 1))
		}
		Expect(result.ChosenModel).To(Equal("combined-target"))
		GinkgoWriter.Printf("native routing: scores=%+v labels=%v candidate=%s\n", result.Decision.LabelScores, result.Labels, result.ChosenModel)
	})
})
