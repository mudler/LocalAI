package concurrency_test

import (
	"fmt"
	"time"

	. "github.com/go-skynet/LocalAI/pkg/concurrency"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg/concurrency unit tests", func() {
	It("can be used to recieve a result across goroutines", func() {
		jr, wjr := NewJobResult[string, string]("foo")
		Expect(jr).ToNot(BeNil())
		Expect(wjr).ToNot(BeNil())
		stallChannel := make(chan struct{})
		go func(jr *JobResult[string, string]) {
			resPtr, err := jr.Wait()
			Expect(err).To(BeNil())
			Expect(jr.Request).ToNot(BeNil())
			Expect(*jr.Request).To(Equal("foo"))
			Expect(resPtr).ToNot(BeNil())
			Expect(*resPtr).To(Equal("bar"))
			close(stallChannel)
		}(jr)
		go func(wjr *WritableJobResult[string, string]) {
			time.Sleep(time.Second * 5)
			wjr.SetResult("bar", nil)
		}(wjr)
		<-stallChannel
	})

	It("can be used to recieve an error across goroutines", func() {
		jr, wjr := NewJobResult[string, string]("foo")
		Expect(jr).ToNot(BeNil())
		Expect(wjr).ToNot(BeNil())
		stallChannel := make(chan struct{})
		go func(jr *JobResult[string, string]) {
			_, err := jr.Wait()
			Expect(jr.Request).To(BeNil())
			Expect(*jr.Request).To(Equal("foo"))
			Expect(err).ToNot(BeNil())
			Expect(err).To(MatchError("test"))
			close(stallChannel)
		}(jr)
		go func(wjr *WritableJobResult[string, string]) {
			time.Sleep(time.Second * 5)
			wjr.SetResult("", fmt.Errorf("test"))
		}(wjr)
		<-stallChannel
	})
})
