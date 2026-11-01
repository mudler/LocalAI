package fanout_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/fanout"
)

func TestFanout(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fanout Suite")
}

var _ = Describe("Hub", func() {
	It("delivers a value to every subscriber of its key and to no other", func() {
		h := fanout.NewHub[string, int](4)
		a, cancelA := h.Subscribe("k")
		b, cancelB := h.Subscribe("k")
		other, cancelO := h.Subscribe("other")
		DeferCleanup(cancelA)
		DeferCleanup(cancelB)
		DeferCleanup(cancelO)
		h.Publish("k", 7)
		Expect(<-a).To(Equal(7))
		Expect(<-b).To(Equal(7))
		Expect(other).ToNot(Receive())
	})

	It("closes a subscriber that falls behind and keeps the others", func() {
		h := fanout.NewHub[string, int](2)
		slow, cancelSlow := h.Subscribe("k")
		fast, cancelFast := h.Subscribe("k")
		DeferCleanup(cancelSlow)
		DeferCleanup(cancelFast)
		for i := range 3 {
			h.Publish("k", i)
			Expect(<-fast).To(Equal(i)) // the fast reader keeps up
		}
		Expect(<-slow).To(Equal(0))
		Expect(<-slow).To(Equal(1))
		_, open := <-slow
		Expect(open).To(BeFalse(), "the third value did not fit")
		h.Publish("k", 9)
		Expect(<-fast).To(Equal(9))
	})

	It("cancels twice without a panic and leaves no key behind", func() {
		h := fanout.NewHub[string, int](1)
		_, cancel := h.Subscribe("k")
		cancel()
		cancel()
		h.Publish("k", 1) // nobody is listening
	})
})

var _ = Describe("Pipe", func() {
	It("ignores a send after the close", func() {
		p := fanout.NewPipe[int](1)
		p.Close()
		p.Close()
		Expect(p.Send(1)).To(BeFalse())
		_, open := <-p.C()
		Expect(open).To(BeFalse())
	})
})
