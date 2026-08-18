package openai

import (
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ttsPipeline", func() {
	It("synthesizes clauses in order and accumulates their audio", func() {
		p := newTTSPipeline(func(segment speechSegment) ([]byte, error) {
			return []byte(segment.Text), nil
		})
		Expect(p.enqueue(speechSegment{Text: "a"})).To(BeTrue())
		Expect(p.enqueue(speechSegment{Text: "b"})).To(BeTrue())
		Expect(p.enqueue(speechSegment{Text: "c"})).To(BeTrue())

		audio, err := p.wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(audio)).To(Equal("abc"))
	})

	It("never blocks the producer even when synthesis is slow", func() {
		var started sync.WaitGroup
		started.Add(1)
		release := make(chan struct{})
		first := true
		p := newTTSPipeline(func(segment speechSegment) ([]byte, error) {
			if first {
				first = false
				started.Done()
				<-release // hold the worker on the first clause
			}
			return []byte(segment.Text), nil
		})

		Expect(p.enqueue(speechSegment{Text: "1"})).To(BeTrue())
		started.Wait() // worker is now blocked synthesizing the first clause

		// Enqueuing many more clauses must return immediately, not block on the
		// stalled worker — this is what keeps the LLM recv loop flowing.
		done := make(chan struct{})
		go func() {
			defer close(done)
			for _, c := range []string{"2", "3", "4", "5"} {
				p.enqueue(speechSegment{Text: c})
			}
		}()
		Eventually(done, time.Second).Should(BeClosed())

		close(release)
		audio, err := p.wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(audio)).To(Equal("12345"))
	})

	It("keeps the first error, stops speaking, and signals the producer to stop", func() {
		boom := errors.New("backend gone")
		var spoken []string
		var mu sync.Mutex
		p := newTTSPipeline(func(segment speechSegment) ([]byte, error) {
			mu.Lock()
			spoken = append(spoken, segment.Text)
			mu.Unlock()
			if segment.Text == "b" {
				return nil, boom
			}
			return []byte(segment.Text), nil
		})

		Expect(p.enqueue(speechSegment{Text: "a"})).To(BeTrue())
		Expect(p.enqueue(speechSegment{Text: "b"})).To(BeTrue())

		// Once the failure is observed, enqueue reports it so the caller stops
		// the prediction; any further clauses are dropped, not spoken.
		Eventually(func() bool { return !p.enqueue(speechSegment{Text: "c"}) }, time.Second).Should(BeTrue())

		_, err := p.wait()
		Expect(err).To(MatchError(boom))

		mu.Lock()
		defer mu.Unlock()
		Expect(spoken).NotTo(ContainElement("c"), "clauses after the failure are not synthesized")
	})

	It("is idempotent: a second wait returns the same result without blocking", func() {
		p := newTTSPipeline(func(segment speechSegment) ([]byte, error) {
			return []byte(segment.Text), nil
		})
		Expect(p.enqueue(speechSegment{Text: "x"})).To(BeTrue())

		audio1, err1 := p.wait()
		// A deferred backstop wait() in the caller runs after the explicit one;
		// it must not block or change the result.
		audio2, err2 := p.wait()

		Expect(err1).NotTo(HaveOccurred())
		Expect(err2).NotTo(HaveOccurred())
		Expect(string(audio1)).To(Equal("x"))
		Expect(string(audio2)).To(Equal("x"))
	})

	It("returns cleanly when no clause was ever enqueued", func() {
		p := newTTSPipeline(func(segment speechSegment) ([]byte, error) {
			return []byte(segment.Text), nil
		})
		audio, err := p.wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(audio).To(BeEmpty())
	})
})
