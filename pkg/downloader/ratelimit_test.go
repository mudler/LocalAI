package downloader

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubCloser struct {
	*bytes.Reader
	closed bool
}

func (s *stubCloser) Close() error {
	s.closed = true
	return nil
}

// oneByteCloser returns at most one byte per Read call, modelling a valid
// but dripping source (slow peer, tiny TLS records). Used to prove Reads
// charge actual bytes rather than the requested chunk.
type oneByteCloser struct {
	*bytes.Reader
}

func (s *oneByteCloser) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return s.Reader.Read(p)
}

func (s *oneByteCloser) Close() error { return nil }

var _ = Describe("DynamicRateLimiter", func() {
	It("is unlimited by default and WaitN returns immediately", func() {
		rl := &DynamicRateLimiter{}
		Expect(rl.Unlimited()).To(BeTrue())
		Expect(rl.WaitN(context.Background(), 1<<20)).To(Succeed())
	})

	It("becomes limited after SetRate and unlimited again on non-positive rates", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(1024)
		Expect(rl.Unlimited()).To(BeFalse())
		rl.SetRate(0)
		Expect(rl.Unlimited()).To(BeTrue())
	})

	It("aborts waiting when the context is cancelled", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(1) // 1 byte/sec forces a real wait
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		err := rl.WaitN(ctx, 1<<20)
		Expect(err).To(MatchError(context.Canceled))
	})

	It("passes reads straight through when unlimited", func() {
		data := bytes.Repeat([]byte("x"), 64*1024)
		r := newRateLimitedReader(&stubCloser{Reader: bytes.NewReader(data)}, &DynamicRateLimiter{}, context.Background())
		buf := make([]byte, len(data))
		n, err := r.Read(buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(len(data)))
		Expect(buf).To(Equal(data))
		Expect(r.Close()).To(Succeed())
	})

	It("throttles chunked reads and honours cancellation", func() {
		data := bytes.Repeat([]byte("y"), 4096)
		rl := &DynamicRateLimiter{}
		rl.SetRate(1024)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := newRateLimitedReader(&stubCloser{Reader: bytes.NewReader(data)}, rl, ctx)
		_, err := r.Read(make([]byte, 2048))
		Expect(err).To(MatchError(context.Canceled))
	})

	It("attaches and reads back through the context helpers", func() {
		rl := &DynamicRateLimiter{}
		ctx := ContextWithRateLimiter(context.Background(), rl)
		Expect(RateLimiterFromContext(ctx)).To(Equal(rl))
		Expect(RateLimiterFromContext(context.Background())).To(BeNil())
	})

	It("exposes a nil-safe Unlimited helper", func() {
		var rl *DynamicRateLimiter
		Expect(rl.Unlimited()).To(BeTrue())
		Expect(rl.WaitN(context.Background(), 10)).To(Succeed())
	})

	It("reads with io.ReadCloser compatibility", func() {
		var _ io.ReadCloser = newRateLimitedReader(&stubCloser{Reader: bytes.NewReader(nil)}, nil, nil)
	})

	It("enforces actual throughput below the 32 KiB chunk size", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(2000)
		// Consume the initial 1s burst so the next wait must actually throttle.
		Expect(rl.WaitN(context.Background(), 2000)).To(Succeed())
		start := time.Now()
		// 1000 bytes at 2000 B/s must take ~0.5s, not return after a capped 1s-or-less shortcut.
		Expect(rl.WaitN(context.Background(), 1000)).To(Succeed())
		elapsed := time.Since(start)
		Expect(elapsed).To(BeNumerically(">=", 400*time.Millisecond), "wait returned too fast, budget not enforced")
		Expect(elapsed).To(BeNumerically("<", 3*time.Second), "wait took too long")
	})

	It("shares one limiter fairly across concurrent readers without overspending", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(2000)
		const readers = 4
		const perReader = 1000 // total 4000, burst covers 2000, debt 2000 => ~1s
		start := time.Now()
		var wg sync.WaitGroup
		errs := make([]error, readers)
		for i := 0; i < readers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				errs[idx] = rl.WaitN(context.Background(), perReader)
			}(i)
		}
		wg.Wait()
		elapsed := time.Since(start)
		for _, err := range errs {
			Expect(err).To(Succeed())
		}
		// If each waiter reset tokens independently, all four would finish in ~0.5s or less.
		// Shared debt requires ~1s for the 2000-byte overdraft.
		Expect(elapsed).To(BeNumerically(">=", 800*time.Millisecond), "concurrent readers overspent shared budget")
		Expect(elapsed).To(BeNumerically("<", 4*time.Second))
	})

	It("observes a limit removal during a wait", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(100) // very slow so the wait would take ~100s without intervention
		done := make(chan error, 1)
		go func() {
			done <- rl.WaitN(context.Background(), 10000)
		}()
		time.Sleep(150 * time.Millisecond)
		rl.SetRate(0) // remove the limit mid-wait
		select {
		case err := <-done:
			Expect(err).To(Succeed(), "removing the limit should release the waiter")
		case <-time.After(2 * time.Second):
			Fail("WaitN did not observe SetRate(0) within 2s")
		}
	})

	It("observes a rate increase during a wait", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(200) // 2000 bytes would need ~9s after burst
		done := make(chan time.Duration, 1)
		start := time.Now()
		go func() {
			_ = rl.WaitN(context.Background(), 2000)
			done <- time.Since(start)
		}()
		time.Sleep(150 * time.Millisecond)
		rl.SetRate(100000) // raise dramatically; waiter should finish promptly
		select {
		case elapsed := <-done:
			Expect(elapsed).To(BeNumerically("<", 3*time.Second), "waiter did not observe rate increase")
		case <-time.After(4 * time.Second):
			Fail("WaitN did not observe SetRate increase within 4s")
		}
	})

	It("does not starve an earlier waiter behind a later large reservation", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(10000)
		// Consume the initial burst so every subsequent byte must be earned.
		Expect(rl.WaitN(context.Background(), 10000)).To(Succeed())

		// Earlier small waiter with a tight deadline.
		firstCtx, firstCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer firstCancel()
		firstDone := make(chan error, 1)
		go func() {
			firstDone <- rl.WaitN(firstCtx, 1000) // ~100ms at 10KB/s
		}()
		// Let the first reservation land before the continuing reader starts.
		time.Sleep(50 * time.Millisecond)

		// Continuing reader: sequential chunked waits totalling 10KB behind it.
		readerDone := make(chan error, 1)
		go func() {
			for i := 0; i < 5; i++ {
				if err := rl.WaitN(context.Background(), 2000); err != nil {
					readerDone <- err
					return
				}
			}
			readerDone <- nil
		}()

		select {
		case err := <-firstDone:
			Expect(err).To(Succeed(), "earlier waiter was starved by the later reservation")
		case <-time.After(500 * time.Millisecond):
			Fail("earlier 1000-byte waiter did not finish within 400ms deadline + margin")
		}
		select {
		case err := <-readerDone:
			Expect(err).To(Succeed())
		case <-time.After(4 * time.Second):
			Fail("continuing reader did not finish")
		}
	})

	It("charges actual bytes on short reads instead of the requested chunk", func() {
		data := bytes.Repeat([]byte("z"), 8)
		rl := &DynamicRateLimiter{}
		rl.SetRate(10000) // 1s burst = 10000 bytes
		r := newRateLimitedReader(&oneByteCloser{Reader: bytes.NewReader(data)}, rl, context.Background())

		buf := make([]byte, 10000)
		n, err := r.Read(buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(1))

		// Only one byte was transferred, so ~9999 of burst must have been
		// refunded: the next 10000-byte chunk needs ~0.1ms, not ~1s.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		r2 := newRateLimitedReader(&oneByteCloser{Reader: bytes.NewReader(data)}, rl, ctx)
		n2, err := r2.Read(make([]byte, 10000))
		Expect(err).ToNot(HaveOccurred(), "second read hit its deadline: short read was not refunded")
		Expect(n2).To(Equal(1))
	})

	It("releases a cancelled reservation so later waiters proceed", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(5000)
		Expect(rl.WaitN(context.Background(), 5000)).To(Succeed()) // consume burst

		ctx, cancel := context.WithCancel(context.Background())
		bigDone := make(chan error, 1)
		go func() {
			bigDone <- rl.WaitN(ctx, 100000) // ~20s of future budget
		}()
		time.Sleep(100 * time.Millisecond) // let the big reservation queue
		cancel()
		select {
		case err := <-bigDone:
			Expect(err).To(MatchError(context.Canceled))
		case <-time.After(2 * time.Second):
			Fail("cancelled waiter did not return")
		}

		// With the big reservation released, 1000 bytes need ~0.2s.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel2()
		Expect(rl.WaitN(ctx2, 1000)).To(Succeed(), "later waiter still blocked by cancelled reservation")
	})

	It("parses human-readable rates and rejects bad input", func() {
		rate, err := ParseRateString("")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(0)))

		rate, err = ParseRateString("0")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(0)))

		rate, err = ParseRateString("unlimited")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(0)))

		rate, err = ParseRateString("-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(0)))

		rate, err = ParseRateString("-5mb")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(0)))

		rate, err = ParseRateString("2mb")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(2 << 20)))

		rate, err = ParseRateString("500kb")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(500 << 10)))

		rate, err = ParseRateString("10b")
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int64(10)))

		_, err = ParseRateString("bogus")
		Expect(err).To(HaveOccurred())

		_, err = ParseRateString("mb")
		Expect(err).To(HaveOccurred())
	})

	It("treats SetRate(0) as unlimited without spinning", func() {
		rl := &DynamicRateLimiter{}
		rl.SetRate(1024)
		Expect(rl.Unlimited()).To(BeFalse())
		rl.SetRate(0)
		Expect(rl.Unlimited()).To(BeTrue())
		start := time.Now()
		Expect(rl.WaitN(context.Background(), 1<<20)).To(Succeed())
		Expect(time.Since(start)).To(BeNumerically("<", 500*time.Millisecond))
	})
})
