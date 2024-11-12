package concurrency_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/mudler/LocalAI/pkg/concurrency"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg/concurrency unit tests", func() {
	It("can be used to receive a result across goroutines", func() {
		jr, wjr := NewJobResult[string, string]("foo")
		Expect(jr).ToNot(BeNil())
		Expect(wjr).ToNot(BeNil())

		go func(wjr *WritableJobResult[string, string]) {
			time.Sleep(time.Second * 5)
			wjr.SetResult("bar", nil)
		}(wjr)

		resPtr, err := jr.Wait(context.Background())
		Expect(err).To(BeNil())
		Expect(jr.Request).ToNot(BeNil())
		Expect(*jr.Request()).To(Equal("foo"))
		Expect(resPtr).ToNot(BeNil())
		Expect(*resPtr).To(Equal("bar"))

	})

	It("can be used to receive an error across goroutines", func() {
		jr, wjr := NewJobResult[string, string]("foo")
		Expect(jr).ToNot(BeNil())
		Expect(wjr).ToNot(BeNil())

		go func(wjr *WritableJobResult[string, string]) {
			time.Sleep(time.Second * 5)
			wjr.SetResult("", fmt.Errorf("test"))
		}(wjr)

		_, err := jr.Wait(context.Background())
		Expect(jr.Request).ToNot(BeNil())
		Expect(*jr.Request()).To(Equal("foo"))
		Expect(err).ToNot(BeNil())
		Expect(err).To(MatchError("test"))
	})

	It("can properly handle timeouts", func() {
		jr, wjr := NewJobResult[string, string]("foo")
		Expect(jr).ToNot(BeNil())
		Expect(wjr).ToNot(BeNil())

		go func(wjr *WritableJobResult[string, string]) {
			time.Sleep(time.Second * 5)
			wjr.SetResult("bar", nil)
		}(wjr)

		timeout1s, c1 := context.WithTimeoutCause(context.Background(), time.Second, fmt.Errorf("timeout"))
		timeout10s, c2 := context.WithTimeoutCause(context.Background(), time.Second*10, fmt.Errorf("timeout"))

		_, err := jr.Wait(timeout1s)
		Expect(jr.Request).ToNot(BeNil())
		Expect(*jr.Request()).To(Equal("foo"))
		Expect(err).ToNot(BeNil())
		Expect(err).To(MatchError(context.DeadlineExceeded))

		resPtr, err := jr.Wait(timeout10s)
		Expect(jr.Request).ToNot(BeNil())
		Expect(*jr.Request()).To(Equal("foo"))
		Expect(err).To(BeNil())
		Expect(resPtr).ToNot(BeNil())
		Expect(*resPtr).To(Equal("bar"))

		// Is this needed? Cleanup Either Way.
		c1()
		c2()
	})
})
