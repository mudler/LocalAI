// Package messagingtest holds the conformance suite every fan-out carrier must
// pass. A carrier is run against it in its own package, so a behaviour one
// carrier has and another lacks is a red spec, not a production surprise.
package messagingtest

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// Factory returns a ready carrier and a cleanup for it.
type Factory func() (messaging.Broadcaster, func())

type blob struct {
	Data string `json:"data"`
}

// collector gathers payloads from a handler, safely across goroutines.
type collector struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (c *collector) handler(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, append([]byte(nil), b...))
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *collector) first() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.msgs[0]
}

// RunBroadcasterConformance registers the suite. Call it from a Describe-level
// position in the carrier's own test package.
func RunBroadcasterConformance(newBus Factory) {
	Describe("Broadcaster conformance", func() {
		var (
			bus     messaging.Broadcaster
			cleanup func()
		)

		BeforeEach(func() { bus, cleanup = newBus() })
		// A factory that skips (no Docker for the NATS run) returns before
		// setting cleanup, so the guard keeps a skip from turning into a panic.
		AfterEach(func() {
			if cleanup != nil {
				cleanup()
			}
		})

		It("delivers a published message to every subscriber", func() {
			a, b := &collector{}, &collector{}
			_, err := bus.Subscribe("jobs.j1.progress", a.handler)
			Expect(err).ToNot(HaveOccurred())
			_, err = bus.Subscribe("jobs.j1.progress", b.handler)
			Expect(err).ToNot(HaveOccurred())

			Expect(bus.Publish("jobs.j1.progress", blob{Data: "x"})).To(Succeed())

			Eventually(a.count, 5*time.Second).Should(Equal(1))
			Eventually(b.count, 5*time.Second).Should(Equal(1))
		})

		It("matches a single-token wildcard and nothing wider", func() {
			c := &collector{}
			_, err := bus.Subscribe("jobs.*.cancel", c.handler)
			Expect(err).ToNot(HaveOccurred())

			Expect(bus.Publish("jobs.abc.result", blob{Data: "no"})).To(Succeed())
			Expect(bus.Publish("jobs.abc.cancel", blob{Data: "yes"})).To(Succeed())

			Eventually(c.count, 5*time.Second).Should(Equal(1))
			Consistently(c.count, 300*time.Millisecond).Should(Equal(1))
		})

		It("stops delivering after Unsubscribe", func() {
			c := &collector{}
			sub, err := bus.Subscribe("gallery.op1.progress", c.handler)
			Expect(err).ToNot(HaveOccurred())
			Expect(bus.Publish("gallery.op1.progress", blob{Data: "1"})).To(Succeed())
			Eventually(c.count, 5*time.Second).Should(Equal(1))

			Expect(sub.Unsubscribe()).To(Succeed())
			Expect(bus.Publish("gallery.op1.progress", blob{Data: "2"})).To(Succeed())
			Consistently(c.count, 300*time.Millisecond).Should(Equal(1))
		})

		It("Unsubscribe removes only its own subscription", func() {
			first, second := &collector{}, &collector{}
			_, err := bus.Subscribe("gallery.op2.progress", first.handler)
			Expect(err).ToNot(HaveOccurred())
			subSecond, err := bus.Subscribe("gallery.op2.progress", second.handler)
			Expect(err).ToNot(HaveOccurred())

			// Dropping the first one is the case a removal keyed on the subject
			// gets right by accident, so drop the second and check the first
			// survives.
			Expect(subSecond.Unsubscribe()).To(Succeed())
			Expect(bus.Publish("gallery.op2.progress", blob{Data: "x"})).To(Succeed())

			Eventually(first.count, 5*time.Second).Should(Equal(1))
			Consistently(second.count, 300*time.Millisecond).Should(Equal(0))
		})

		It("refuses a subject outside the served roots on publish and subscribe", func() {
			err := bus.Publish("bogus.thing", blob{Data: "x"})
			Expect(errors.Is(err, messaging.ErrUnservedSubject)).To(BeTrue(), "publish: %v", err)

			_, err = bus.Subscribe("bogus.thing", func([]byte) {})
			Expect(errors.Is(err, messaging.ErrUnservedSubject)).To(BeTrue(), "subscribe: %v", err)
		})

		It("refuses a multi-token wildcard subscription", func() {
			_, err := bus.Subscribe("jobs.>", func([]byte) {})
			Expect(errors.Is(err, messaging.ErrUnsupportedWildcard)).To(BeTrue(), "got %v", err)
		})

		DescribeTable("delivers payloads past the PostgreSQL notify cap intact",
			func(size int) {
				c := &collector{}
				_, err := bus.Subscribe("cache.invalidate.models", c.handler)
				Expect(err).ToNot(HaveOccurred())

				want := strings.Repeat("a", size)
				Expect(bus.Publish("cache.invalidate.models", blob{Data: want})).To(Succeed())

				Eventually(c.count, 10*time.Second).Should(Equal(1))
				var got blob
				Expect(json.Unmarshal(c.first(), &got)).To(Succeed())
				Expect(got.Data).To(HaveLen(size))
				Expect(got.Data).To(Equal(want))
			},
			Entry("just under the notify cap", 7900),
			Entry("over the notify cap", 64*1024),
		)
	})
}
