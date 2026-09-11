package grpc

import (
	"context"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("stream cleanup", func() {
	It("runs registered cleanup exactly once at stream termination", func() {
		var calls atomic.Int32
		lifecycle := newStreamCleanup(context.Background(), func() { calls.Add(1) })
		lifecycle.add(func() { calls.Add(1) })

		lifecycle.finish()
		lifecycle.finish()

		Expect(calls.Load()).To(Equal(int32(2)))
	})

	It("runs callbacks registered after termination immediately", func() {
		var calls atomic.Int32
		lifecycle := newStreamCleanup(context.Background(), nil)
		lifecycle.finish()
		lifecycle.add(func() { calls.Add(1) })
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("terminates when the stream context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		var calls atomic.Int32
		lifecycle := newStreamCleanup(ctx, func() { calls.Add(1) })
		cancel()
		Eventually(calls.Load).Should(Equal(int32(1)))
		lifecycle.finish()
		Expect(calls.Load()).To(Equal(int32(1)))
	})
})
