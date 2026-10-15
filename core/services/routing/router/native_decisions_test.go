// SPDX-License-Identifier: MIT
package router_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type decisionFunc func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error)

func (f decisionFunc) Decide(ctx context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	return f(ctx, r)
}
func noul(v float64) schema.SystemOneAnswer { return schema.SystemOneAnswer{Type: "noul", Noul: &v} }

var _ = Describe("native decisions classifier", func() {
	policies := []router.ScorePolicy{{Label: "code", Description: "writing code"}, {Label: "private", Description: "private information"}}
	var answers map[string]schema.SystemOneAnswer
	var runner decisionFunc
	BeforeEach(func() {
		answers = map[string]schema.SystemOneAnswer{"p0": noul(.8), "p1": noul(.7)}
		runner = func(_ context.Context, req *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
			Expect(string(req.State)).To(Equal(`"prompt"`))
			Expect(req.Questions).To(HaveLen(2))
			for _, q := range req.Questions {
				Expect(q.Type).To(Equal("noul"))
				var criteria map[string]string
				Expect(json.Unmarshal(q.Criteria, &criteria)).To(Succeed())
				Expect(criteria).To(HaveKey("false"))
				Expect(criteria).To(HaveKey("true"))
			}
			return &schema.SystemOneResponse{Answers: answers}, nil
		}
	})
	It("preserves overlapping independent probabilities and policy ordering", func() {
		c, err := router.NewDecisionsClassifier(policies, runner, 0)
		Expect(err).NotTo(HaveOccurred())
		d, err := c.Classify(context.Background(), router.Probe{Prompt: "prompt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Labels).To(Equal([]string{"code", "private"}))
		Expect(d.Score).To(Equal(.8))
		Expect(d.LabelScores).To(Equal([]router.LabelScore{{Label: "code", Score: .8}, {Label: "private", Score: .7}}))
		Expect(d.ActivationThreshold).To(Equal(.5))
	})
	It("accepts zero and threshold equality; abstains without top-one", func() {
		c, err := router.NewDecisionsClassifier(policies, runner, .5)
		Expect(err).NotTo(HaveOccurred())
		answers["p0"] = noul(0)
		answers["p1"] = noul(.5)
		d, err := c.Classify(context.Background(), router.Probe{Prompt: "prompt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Labels).To(Equal([]string{"private"}))
		answers["p1"] = noul(.49)
		d, err = c.Classify(context.Background(), router.Probe{Prompt: "prompt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Labels).To(BeEmpty())
	})
	It("rejects missing extra null wrong-type nonfinite and out-of-range answers", func() {
		c, err := router.NewDecisionsClassifier(policies, runner, 0)
		Expect(err).NotTo(HaveOccurred())
		for _, a := range []map[string]schema.SystemOneAnswer{
			nil, {"p0": noul(.8)}, {"p0": noul(.8), "p1": noul(.7), "extra": noul(.9)},
			{"p0": {Type: "noul"}, "p1": noul(.7)}, {"p0": {Type: "score", Noul: noul(.5).Noul}, "p1": noul(.7)},
			{"p0": noul(math.NaN()), "p1": noul(.7)}, {"p0": noul(math.Inf(1)), "p1": noul(.7)}, {"p0": noul(-.1), "p1": noul(.7)}, {"p0": noul(1.1), "p1": noul(.7)},
		} {
			answers = a
			_, err = c.Classify(context.Background(), router.Probe{Prompt: "prompt"})
			Expect(err).To(HaveOccurred())
		}
	})
	It("distinguishes wire null and missing noul from zero and rejects substituted IDs", func() {
		c, err := router.NewDecisionsClassifier(policies, runner, 0)
		Expect(err).NotTo(HaveOccurred())
		for _, raw := range []string{`{"p0":null,"p1":{"type":"noul","noul":0.7}}`, `{"p0":{"type":"noul","noul":null},"p1":{"type":"noul","noul":0.7}}`, `{"p0":{"type":"noul","noul":0.8},"other":{"type":"noul","noul":0.7}}`} {
			Expect(json.Unmarshal([]byte(raw), &answers)).To(Succeed())
			_, err = c.Classify(context.Background(), router.Probe{Prompt: "prompt"})
			Expect(err).To(HaveOccurred())
			answers = nil
		}
	})

	It("validates labels thresholds questions and request bounds", func() {
		for _, v := range []float64{-.1, 1.1, math.NaN(), math.Inf(1)} {
			_, err := router.NewDecisionsClassifier(policies, runner, v)
			Expect(err).To(HaveOccurred())
		}
		for _, p := range [][]router.ScorePolicy{nil, {{Label: "same", Description: "a"}, {Label: "same", Description: "b"}}, {{Label: " ", Description: "a"}}, {{Label: "x", Description: " "}}, make([]router.ScorePolicy, 65)} {
			_, err := router.NewDecisionsClassifier(p, runner, 0)
			Expect(err).To(HaveOccurred())
		}
		c, err := router.NewDecisionsClassifier(policies, runner, 0)
		Expect(err).NotTo(HaveOccurred())
		_, err = c.Classify(context.Background(), router.Probe{Prompt: strings.Repeat("x", 65536)})
		Expect(err).To(HaveOccurred())
	})
	It("does not call the adapter after parent cancellation", func() {
		c, err := router.NewDecisionsClassifier(policies, decisionFunc(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
			Fail("called")
			return nil, nil
		}), 0)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = c.Classify(ctx, router.Probe{Prompt: "prompt"})
		Expect(err).To(MatchError(context.Canceled))
	})
	It("uses first-superset and falls back on overload, context rejection or abstention, not parent cancellation", func() {
		cfg := &config.ModelConfig{Name: "route", Router: config.RouterConfig{Classifier: "decisions", Fallback: "fallback", Candidates: []config.RouterCandidate{{Model: "small", Labels: []string{"code"}}, {Model: "both", Labels: []string{"code", "private"}}}}}
		loads := 0
		load := func(name string) (*config.ModelConfig, error) { loads++; return &config.ModelConfig{Name: name}, nil }
		c, err := router.NewDecisionsClassifier(policies, runner, 0)
		Expect(err).NotTo(HaveOccurred())
		r, err := router.Resolve(context.Background(), cfg, c, load, router.Probe{Prompt: "prompt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ChosenModel).To(Equal("both"))
		for _, failure := range []error{errors.New("native decision operation capacity reached"), errors.New("native context exceeded")} {
			c, err = router.NewDecisionsClassifier(policies, decisionFunc(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
				return nil, failure
			}), 0)
			Expect(err).NotTo(HaveOccurred())
			r, err = router.Resolve(context.Background(), cfg, c, load, router.Probe{Prompt: "prompt"})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.UsedFallback).To(BeTrue())
		}
		answers["p0"] = noul(0)
		answers["p1"] = noul(0)
		c, err = router.NewDecisionsClassifier(policies, runner, 0)
		Expect(err).NotTo(HaveOccurred())
		r, err = router.Resolve(context.Background(), cfg, c, load, router.Probe{Prompt: "prompt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.UsedFallback).To(BeTrue())
		ctx, cancel := context.WithCancel(context.Background())
		c, err = router.NewDecisionsClassifier(policies, decisionFunc(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
			cancel()
			return nil, context.Canceled
		}), 0)
		Expect(err).NotTo(HaveOccurred())
		loads = 0
		_, err = router.Resolve(ctx, cfg, c, load, router.Probe{Prompt: "prompt"})
		Expect(err).To(MatchError(context.Canceled))
		Expect(loads).To(BeZero())
		cancel()
		loads = 0
		_, err = router.Resolve(ctx, cfg, c, load, router.Probe{})
		Expect(err).To(MatchError(context.Canceled))
		_, err = router.Resolve(ctx, cfg, nil, load, router.Probe{})
		Expect(err).To(MatchError(context.Canceled))
		Expect(loads).To(BeZero())
	})
})

var _ = Describe("multimodal native routing", func() {
	It("passes image-only structured state unchanged and rejects invalid input before runner", func() {
		// Portable valid 1x1 PNG, generated with the standard encoder below.
		var b bytes.Buffer
		Expect(png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))).To(Succeed())
		u := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
		state, _ := json.Marshal([]any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": u}}}}})
		calls := 0
		c, err := router.NewDecisionsClassifier([]router.ScorePolicy{{Label: "visual", Description: "visual content"}}, decisionFunc(func(_ context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
			calls++
			Expect(r.State).To(Equal(json.RawMessage(state)))
			Expect(r.Images).To(BeEmpty())
			return &schema.SystemOneResponse{Answers: map[string]schema.SystemOneAnswer{"p0": noul(.9)}}, nil
		}), 0)
		Expect(err).NotTo(HaveOccurred())
		d, err := c.Classify(context.Background(), router.Probe{State: state})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Labels).To(Equal([]string{"visual"}))
		_, err = c.Classify(context.Background(), router.Probe{State: json.RawMessage(`{}`), Images: json.RawMessage(`["https://example.org/x.png"]`)})
		Expect(err).To(HaveOccurred())
		Expect(calls).To(Equal(1))
	})
	It("rejects images in every text classifier before cache trimming or model use", func() {
		p := router.Probe{Prompt: "same text", State: json.RawMessage(`{}`), Images: json.RawMessage(`["data:image/png;base64,AA=="]`)}
		for _, c := range []router.Classifier{&router.ScoreClassifier{}, &router.RerankClassifier{}, &router.KNNClassifier{}} {
			_, err := c.Classify(context.Background(), p)
			Expect(err).To(MatchError(ContainSubstring("does not support image")))
		}
	})
})
