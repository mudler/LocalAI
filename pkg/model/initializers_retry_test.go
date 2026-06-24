package model

import (
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("retryEnforce", func() {
	It("returns immediately when the first attempt is satisfied", func() {
		var calls atomic.Int32
		retryEnforce(func() EnforceLRULimitResult {
			calls.Add(1)
			return EnforceLRULimitResult{}
		}, 5, 1*time.Millisecond, "test")
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("retries until NeedMore clears", func() {
		var calls atomic.Int32
		retryEnforce(func() EnforceLRULimitResult {
			n := calls.Add(1)
			if n < 3 {
				return EnforceLRULimitResult{NeedMore: true}
			}
			return EnforceLRULimitResult{EvictedCount: 1}
		}, 5, 1*time.Millisecond, "test")
		Expect(calls.Load()).To(Equal(int32(3)))
	})

	It("stops after maxRetries when NeedMore never clears", func() {
		var calls atomic.Int32
		retryEnforce(func() EnforceLRULimitResult {
			calls.Add(1)
			return EnforceLRULimitResult{NeedMore: true}
		}, 4, 1*time.Millisecond, "test")
		Expect(calls.Load()).To(Equal(int32(4)))
	})

	It("treats maxRetries <= 0 as a no-op (no calls)", func() {
		var calls atomic.Int32
		retryEnforce(func() EnforceLRULimitResult {
			calls.Add(1)
			return EnforceLRULimitResult{}
		}, 0, 1*time.Millisecond, "test")
		Expect(calls.Load()).To(Equal(int32(0)))
	})
})
