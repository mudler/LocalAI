package main

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("batcher", func() {
	echoReply := func(reqs []*batchRequest) {
		for _, r := range reqs {
			r.reply <- batchReply{json: r.tag}
		}
	}

	It("coalesces concurrent submits into batches", func() {
		var mu sync.Mutex
		var sizes []int
		run := func(reqs []*batchRequest) {
			mu.Lock()
			sizes = append(sizes, len(reqs))
			mu.Unlock()
			echoReply(reqs)
		}
		b := newBatcher(4, 50*time.Millisecond, run)
		stop := make(chan struct{})
		go b.run(stop)
		defer close(stop)

		const N = 4
		var wg sync.WaitGroup
		got := make([]string, N)
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rep := make(chan batchReply, 1)
				b.submit <- &batchRequest{tag: string(rune('a' + i)), reply: rep}
				got[i] = (<-rep).json
			}(i)
		}
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		total, maxBatch := 0, 0
		for _, s := range sizes {
			total += s
			if s > maxBatch {
				maxBatch = s
			}
		}
		Expect(total).To(Equal(N))
		Expect(maxBatch).To(BeNumerically(">=", 2), "expected at least one batch to coalesce >1 request")
	})

	It("dispatches when max size is reached", func() {
		dispatched := make(chan int, 8)
		run := func(reqs []*batchRequest) {
			dispatched <- len(reqs)
			echoReply(reqs)
		}
		b := newBatcher(2, time.Hour, run) // huge window: only size can trigger
		stop := make(chan struct{})
		go b.run(stop)
		defer close(stop)
		for i := 0; i < 2; i++ {
			rep := make(chan batchReply, 1)
			b.submit <- &batchRequest{tag: "x", reply: rep}
			go func(rep chan batchReply) { <-rep }(rep)
		}
		Eventually(dispatched, "2s").Should(Receive(Equal(2)))
	})

	It("dispatches when the wait window elapses", func() {
		dispatched := make(chan int, 8)
		run := func(reqs []*batchRequest) {
			dispatched <- len(reqs)
			echoReply(reqs)
		}
		b := newBatcher(8, 20*time.Millisecond, run) // size unreachable; window fires
		stop := make(chan struct{})
		go b.run(stop)
		defer close(stop)
		rep := make(chan batchReply, 1)
		b.submit <- &batchRequest{tag: "x", reply: rep}
		go func() { <-rep }()
		Eventually(dispatched, "2s").Should(Receive(Equal(1)))
	})

	It("bypasses batching when max size is 1", func() {
		dispatched := make(chan int, 8)
		run := func(reqs []*batchRequest) {
			dispatched <- len(reqs)
			echoReply(reqs)
		}
		b := newBatcher(1, time.Hour, run) // size 1 => immediate dispatch
		stop := make(chan struct{})
		go b.run(stop)
		defer close(stop)
		rep := make(chan batchReply, 1)
		b.submit <- &batchRequest{tag: "x", reply: rep}
		go func() { <-rep }()
		Eventually(dispatched, "2s").Should(Receive(Equal(1)))
	})

	It("never coalesces requests with different languages into one batch", func() {
		// parakeet.cpp's batched C-API takes ONE target_lang per batch, so the
		// dispatcher must keep every dispatched batch single-language. Submit a
		// mix of languages and assert (a) no batch ever carries more than one
		// distinct language and (b) every submitted request still gets a reply
		// (the mismatched carry-over is never dropped).
		var mu sync.Mutex
		var langsPerBatch [][]string
		run := func(reqs []*batchRequest) {
			seen := map[string]struct{}{}
			var distinct []string
			for _, r := range reqs {
				if _, ok := seen[r.language]; !ok {
					seen[r.language] = struct{}{}
					distinct = append(distinct, r.language)
				}
			}
			mu.Lock()
			langsPerBatch = append(langsPerBatch, distinct)
			mu.Unlock()
			echoReply(reqs)
		}
		// Large window + size so the fill loop stays open across submits and the
		// language constraint (not the timer) is what splits the batches.
		b := newBatcher(16, 200*time.Millisecond, run)
		stop := make(chan struct{})
		go b.run(stop)
		defer close(stop)

		langs := []string{"en", "en", "de", "de", "en", "fr", "fr"}
		const N = 7
		var wg sync.WaitGroup
		got := make([]string, N)
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rep := make(chan batchReply, 1)
				b.submit <- &batchRequest{tag: string(rune('a' + i)), language: langs[i], reply: rep}
				got[i] = (<-rep).json
			}(i)
		}
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		// Invariant: every dispatched batch is single-language.
		for _, distinct := range langsPerBatch {
			Expect(len(distinct)).To(Equal(1), "a batch coalesced more than one language: %v", distinct)
		}
		// Liveness: every request got a reply (carry-over never stranded).
		for i := 0; i < N; i++ {
			Expect(got[i]).To(Equal(string(rune('a' + i))))
		}
	})
})
