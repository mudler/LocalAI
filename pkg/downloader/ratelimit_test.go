package downloader

import (
	"bytes"
	"context"
	"io"
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
})
