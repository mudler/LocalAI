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

// Carrier is what a factory returns: two instances of one carrier over the same
// backing store, as two replicas of a deployment would have, and what the suite
// needs to know about the carrier.
type Carrier struct {
	// Bus and Peer are two ends of the carrier. A message published on one must
	// reach a subscriber on the other. A carrier that has no second end, such as
	// an in-memory double, may return the same value twice.
	Bus, Peer messaging.Broadcaster
	// Cleanup releases both ends.
	Cleanup func()
	// ServesControlRoots is true when the carrier accepts the control roots
	// (nodes, mcp), which carry request and reply traffic. A carrier that has no
	// request and reply refuses them with messaging.ErrUnservedSubject.
	ServesControlRoots bool
	// Dropped returns how many messages the carrier has dropped since it was
	// built because a consumer fell behind. It is nil for a carrier that does
	// not count.
	Dropped func() uint64
}

// Factory returns a ready carrier. A factory that cannot build one calls Skip or
// Fail itself.
type Factory func() Carrier

// BroadcastRootSubjects names one subject for each broadcast root that every
// carrier serves. A spec in the messaging package asserts that the keys are the
// roots that messaging.ValidateSubject accepts as broadcast roots, so a root
// added there without a row here turns a spec red.
var BroadcastRootSubjects = map[string]string{
	"jobs":        "jobs.j1.progress",
	"agent":       "agent.a1.events.u1",
	"gallery":     "gallery.g1.progress",
	"cache":       "cache.invalidate.models",
	"staging":     "staging.m1.progress",
	"prefixcache": "prefixcache.observe",
	"responses":   "responses.r1.cancel",
	"state":       "state.s1",
	"finetune":    "finetune.f1.progress",
}

// controlRootSubjects names one subject for each control root.
var controlRootSubjects = map[string]string{
	"nodes": "nodes.n1.backend.stop",
	"mcp":   "mcp.a1.tools",
}

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
			peer    messaging.Broadcaster
			carrier Carrier
		)

		BeforeEach(func() {
			carrier = newBus()
			bus, peer = carrier.Bus, carrier.Peer
		})
		// A factory that skips (no Docker for the NATS run) returns before
		// setting a cleanup, so the guard keeps a skip from turning into a panic.
		AfterEach(func() {
			if carrier.Cleanup != nil {
				carrier.Cleanup()
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

		DescribeTable("delivers payloads of every size around the PostgreSQL notify cap intact",
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
			Entry("one byte", 1),
			Entry("the last size that fits in a notification", 7999),
			Entry("the first size that does not", 8000),
			Entry("past the cap", 64*1024),
		)

		It("refuses a payload above the bound that every carrier shares", func() {
			c := &collector{}
			_, err := bus.Subscribe("cache.invalidate.models", c.handler)
			Expect(err).ToNot(HaveOccurred())

			err = bus.Publish("cache.invalidate.models", blob{Data: strings.Repeat("a", messaging.MaxBroadcastBytes)})
			Expect(errors.Is(err, messaging.ErrPayloadTooLarge)).To(BeTrue(), "got %v", err)
			Consistently(c.count, 300*time.Millisecond).Should(BeZero())
		})

		DescribeTable("carries a message on every broadcast root",
			func(root string) {
				subject := BroadcastRootSubjects[root]
				c := &collector{}
				_, err := peer.Subscribe(subject, c.handler)
				Expect(err).ToNot(HaveOccurred())

				Expect(bus.Publish(subject, blob{Data: root})).To(Succeed())

				Eventually(c.count, 5*time.Second).Should(Equal(1))
			},
			Entry("jobs", "jobs"),
			Entry("agent", "agent"),
			Entry("gallery", "gallery"),
			Entry("cache", "cache"),
			Entry("staging", "staging"),
			Entry("prefixcache", "prefixcache"),
			Entry("responses", "responses"),
			Entry("state", "state"),
			Entry("finetune", "finetune"),
		)

		It("carries a message to a subscriber on the other end of the carrier", func() {
			here, there := &collector{}, &collector{}
			_, err := bus.Subscribe("jobs.j2.result", here.handler)
			Expect(err).ToNot(HaveOccurred())
			_, err = peer.Subscribe("jobs.j2.result", there.handler)
			Expect(err).ToNot(HaveOccurred())

			Expect(bus.Publish("jobs.j2.result", blob{Data: "x"})).To(Succeed())

			Eventually(there.count, 5*time.Second).Should(Equal(1))
			Eventually(here.count, 5*time.Second).Should(Equal(1))
		})

		It("carries a payload past the notify cap to the other end", func() {
			c := &collector{}
			_, err := peer.Subscribe("jobs.j3.result", c.handler)
			Expect(err).ToNot(HaveOccurred())

			want := strings.Repeat("b", 64*1024)
			Expect(bus.Publish("jobs.j3.result", blob{Data: want})).To(Succeed())

			Eventually(c.count, 10*time.Second).Should(Equal(1))
			var got blob
			Expect(json.Unmarshal(c.first(), &got)).To(Succeed())
			Expect(got.Data).To(Equal(want))
		})

		DescribeTable("handles the control roots as the carrier declares",
			func(root string) {
				subject := controlRootSubjects[root]
				err := bus.Publish(subject, blob{Data: "x"})
				if carrier.ServesControlRoots {
					Expect(err).ToNot(HaveOccurred())
					return
				}
				Expect(errors.Is(err, messaging.ErrUnservedSubject)).To(BeTrue(), "publish: %v", err)
				_, err = bus.Subscribe(subject, func([]byte) {})
				Expect(errors.Is(err, messaging.ErrUnservedSubject)).To(BeTrue(), "subscribe: %v", err)
			},
			Entry("nodes", "nodes"),
			Entry("mcp", "mcp"),
		)

		It("reports no dropped message when every consumer keeps up", func() {
			if carrier.Dropped == nil {
				Skip("the carrier does not count dropped messages")
			}
			c := &collector{}
			_, err := bus.Subscribe("gallery.g2.progress", c.handler)
			Expect(err).ToNot(HaveOccurred())

			Expect(bus.Publish("gallery.g2.progress", blob{Data: "x"})).To(Succeed())

			Eventually(c.count, 5*time.Second).Should(Equal(1))
			Expect(carrier.Dropped()).To(BeZero())
		})
	})
}
