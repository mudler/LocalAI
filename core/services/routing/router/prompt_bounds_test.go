// SPDX-License-Identifier: MIT
package router

import (
	"context"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Direct prompt escape bounds", func() {
	for _, native := range []bool{false, true} {
		It("rejects escaped expansion before allocation", func() {
			p := Probe{Prompt: strings.Repeat("\x00", systemone.MaxImageBodyBytes)}
			called := false
			c, err := NewDecisionsClassifier([]ScorePolicy{{Label: "a", Description: "a"}}, retryRunner(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
				called = true
				return nil, nil
			}), 0)
			Expect(err).NotTo(HaveOccurred())
			runtime.GC()
			var a, b runtime.MemStats
			runtime.ReadMemStats(&a)
			if native {
				_, err = c.Classify(context.Background(), p)
			} else {
				_, err = p.HasImages(context.Background())
			}
			runtime.ReadMemStats(&b)
			Expect(err).To(HaveOccurred())
			Expect(called).To(BeFalse())
			Expect(b.TotalAlloc - a.TotalAlloc).To(BeNumerically("<", systemone.MaxBodyBytes))
		})
	}
	It("accepts the serialized boundary and ordinary text limit", func() {
		available := systemone.MaxImageBodyBytes - 2
		text := strings.Repeat("\x00", available/6) + strings.Repeat("a", available%6)
		r, err := (Probe{Prompt: text}).decisionRequest()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(HaveLen(systemone.MaxImageBodyBytes))
		_, err = (Probe{Prompt: text + "a"}).decisionRequest()
		Expect(err).To(HaveOccurred())
		images, err := (Probe{Prompt: strings.Repeat("a", systemone.MaxBodyBytes)}).HasImages(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(images).To(BeFalse())
	})
	It("uses only configured runtime fallback for oversized prompts", func() {
		c, err := NewDecisionsClassifier([]ScorePolicy{{Label: "a", Description: "a"}}, retryRunner(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
			Fail("runner called")
			return nil, nil
		}), 0)
		Expect(err).NotTo(HaveOccurred())
		cfg := &config.ModelConfig{Name: "route", Router: config.RouterConfig{Classifier: "decisions", Candidates: []config.RouterCandidate{{Model: "candidate", Labels: []string{"a"}}}}}
		loaded := []string{}
		loader := func(name string) (*config.ModelConfig, error) {
			loaded = append(loaded, name)
			return &config.ModelConfig{Name: name}, nil
		}
		p := Probe{Prompt: strings.Repeat("\x00", systemone.MaxImageBodyBytes)}
		_, err = Resolve(context.Background(), cfg, c, loader, p)
		Expect(err).To(HaveOccurred())
		Expect(loaded).To(BeEmpty())
		cfg.Router.Fallback = "fallback"
		result, err := Resolve(context.Background(), cfg, c, loader, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.UsedFallback).To(BeTrue())
		Expect(loaded).To(Equal([]string{"fallback"}))
	})

})
