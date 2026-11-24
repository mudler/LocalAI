// SPDX-License-Identifier: MIT

package middleware

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("API trace summary", func() {
	exchange := func(age time.Duration, status int, dur time.Duration) APIExchange {
		return APIExchange{
			Timestamp: time.Now().Add(-age),
			Duration:  dur,
			Response:  APIExchangeResponse{Status: status},
		}
	}

	It("counts only what falls inside the window", func() {
		traces := []APIExchange{
			exchange(1*time.Hour, 200, 10*time.Millisecond),
			exchange(2*time.Hour, 200, 10*time.Millisecond),
			// Older than the window: must not be counted at all.
			exchange(48*time.Hour, 500, 10*time.Millisecond),
		}
		s := summarize(traces, 24*time.Hour, 6)
		Expect(s.Total).To(Equal(2))
		Expect(s.Errors).To(BeZero())
	})

	It("treats 5xx and a transport error as failures, but not 4xx", func() {
		traces := []APIExchange{
			exchange(time.Minute, 500, time.Millisecond),
			exchange(time.Minute, 503, time.Millisecond),
			// A client sending a bad request is not the server failing.
			exchange(time.Minute, 404, time.Millisecond),
			exchange(time.Minute, 200, time.Millisecond),
		}
		traces[3].Error = "connection reset"

		s := summarize(traces, 24*time.Hour, 6)
		Expect(s.Total).To(Equal(4))
		Expect(s.Errors).To(Equal(3))
	})

	It("reports p95 as a real percentile rather than the slowest request", func() {
		traces := make([]APIExchange, 0, 100)
		for i := 1; i <= 100; i++ {
			traces = append(traces, exchange(time.Minute, 200, time.Duration(i)*time.Millisecond))
		}
		s := summarize(traces, 24*time.Hour, 6)
		// 95th of 1..100ms, not the 100ms max.
		Expect(s.P95Millis).To(BeNumerically("~", 95, 1))
	})

	It("buckets oldest-first so a sparkline reads left to right", func() {
		traces := []APIExchange{
			exchange(30*time.Minute, 200, time.Millisecond),
			exchange(30*time.Minute, 200, time.Millisecond),
			exchange(5*time.Hour, 200, time.Millisecond),
		}
		s := summarize(traces, 6*time.Hour, 6)
		Expect(s.Buckets).To(HaveLen(6))
		Expect(s.Buckets[0].Count).To(Equal(1), "the 5h-old request lands in the first bucket")
		Expect(s.Buckets[5].Count).To(Equal(2), "the recent pair lands in the last")
	})

	It("returns an empty, non-nil summary when nothing has been traced", func() {
		s := summarize(nil, 24*time.Hour, 6)
		Expect(s.Total).To(BeZero())
		Expect(s.Errors).To(BeZero())
		Expect(s.P95Millis).To(BeZero())
		// A nil slice serialises as null and breaks .map() in the browser.
		Expect(s.Buckets).NotTo(BeNil())
		Expect(s.Buckets).To(HaveLen(6))
	})
})
