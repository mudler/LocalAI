// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("internal decision runner", func() {
	var runner *decisionRunner
	var calls atomic.Int32
	var cfg config.ModelConfig
	var req *schema.SystemOneRequest
	BeforeEach(func() {
		Eventually(func() int { return len(decisionOperations) }).Should(BeZero())
		calls.Store(0)
		cfg = config.ModelConfig{}
		cfg.Name = "native"
		cfg.Backend = "vllm-cpp"
		flags := config.FLAG_DECISIONS
		cfg.KnownUsecases = &flags
		req = &schema.SystemOneRequest{State: json.RawMessage(`"text"`), Questions: map[string]schema.SystemOneQuestion{"q": {Type: "noul"}}}
		runner = &decisionRunner{modelName: "native", lookup: func(string) *config.ModelConfig { return &cfg }, load: func(body string, c config.ModelConfig) (func(context.Context) (string, error), error) {
			defer GinkgoRecover()
			calls.Add(1)
			var sent schema.SystemOneRequest
			Expect(json.Unmarshal([]byte(body), &sent)).To(Succeed())
			Expect(sent.Model).To(Equal("native"))
			return func(context.Context) (string, error) { return `{"answers":{"q":{"type":"noul","noul":0.75}}}`, nil }, nil
		}}
	})

	It("rejects admission saturation before inspecting caller data", func() {
		var releases []func()
		defer func() {
			for _, r := range releases {
				r()
			}
		}()
		for i := 0; i < systemone.MaxAdmissions; i++ {
			r, err := systemone.AcquireAdmission(context.Background())
			Expect(err).NotTo(HaveOccurred())
			releases = append(releases, r)
		}
		req.State = json.RawMessage(`invalid`)
		_, err := runner.Decide(context.Background(), req)
		Expect(err).To(MatchError(systemone.ErrAdmissionCapacity))
		Expect(calls.Load()).To(BeZero())
	})
	It("uses a named internal call and preserves numeric noul without probabilities", func() {
		result, err := runner.Decide(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(*result.Answers["q"].Noul).To(Equal(.75))
		Expect(req.Model).To(BeEmpty())
		Expect(calls.Load()).To(Equal(int32(1)))
	})
	It("revalidates model config and bounds requests before loading", func() {
		cfg.KnownUsecases = nil
		_, err := runner.Decide(context.Background(), req)
		Expect(err).To(HaveOccurred())
		Expect(calls.Load()).To(BeZero())
		flags := config.FLAG_DECISIONS
		cfg.KnownUsecases = &flags
		req.State, _ = json.Marshal(strings.Repeat("a", 65536))
		_, err = runner.Decide(context.Background(), req)
		Expect(err).To(HaveOccurred())
		Expect(calls.Load()).To(BeZero())
	})
	It("returns cancellation while an uncancellable load is still running", func() {
		started := make(chan struct{})
		finish := make(chan struct{})
		defer close(finish)
		runner.load = func(string, config.ModelConfig) (func(context.Context) (string, error), error) {
			close(started)
			<-finish
			return func(context.Context) (string, error) { calls.Add(1); return `{}`, nil }, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := runner.Decide(ctx, req); done <- err }()
		Eventually(started, time.Second).Should(BeClosed())
		cancel()
		Eventually(done, time.Second).Should(Receive(Equal(context.Canceled)))
		Expect(calls.Load()).To(BeZero())
	})
	It("bounds abandoned loads across new runner instances", func() {
		Eventually(func() int { return len(decisionOperations) }).Should(BeZero())
		finish := make(chan struct{})
		defer close(finish)
		started := make(chan struct{}, cap(decisionOperations))
		load := func(string, config.ModelConfig) (func(context.Context) (string, error), error) {
			started <- struct{}{}
			<-finish
			return func(context.Context) (string, error) { return `{}`, nil }, nil
		}
		// Cancellation does not free the global permit until Load actually ends.
		for i := 0; i < cap(decisionOperations); i++ {
			copyRunner := *runner
			copyRunner.load = load
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, err := copyRunner.Decide(ctx, req); done <- err }()
			Eventually(started).Should(Receive())
			cancel()
			Eventually(done).Should(Receive(Equal(context.Canceled)))
		}
		_, err := runner.Decide(context.Background(), req)
		Expect(err).To(MatchError(ContainSubstring("capacity reached")))
		Expect(calls.Load()).To(BeZero())
	})

	It("does not load for an already cancelled parent", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := runner.Decide(ctx, req)
		Expect(err).To(MatchError(context.Canceled))
		Expect(calls.Load()).To(BeZero())
	})
	It("rejects malformed and oversized responses", func() {
		for _, body := range []string{`null`, `[]`, `{}`, `{"answers":null}`, `{"answers":{}} trailing`, `{"answers":{"q":{"type":"noul","noul":"0.5"}}}`, `{"answers":{"q":{"type":"noul","noul":true}}}`, `{"answers":{"q":{"type":"noul","noul":1e999}}}`, strings.Repeat("x", 65537)} {
			runner.load = func(string, config.ModelConfig) (func(context.Context) (string, error), error) {
				return func(context.Context) (string, error) { return body, nil }, nil
			}
			_, err := runner.Decide(context.Background(), req)
			Expect(err).To(HaveOccurred())
		}
	})
})
