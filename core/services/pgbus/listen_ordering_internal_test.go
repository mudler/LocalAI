// SPDX-License-Identifier: MIT

package pgbus

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// Internal on purpose. What these specs are about is the ORDER in which two
// registrations reach the connection, and that order is not observable from
// outside the package: a channel that is subscribed but not listened behaves
// exactly like a deployment where nobody is publishing.
//
// They drive that order through the barrier seam rather than by racing
// goroutines and hoping. The natural window here was measured at zero hits in
// forty attempts and ten out of ten once widened by hand, so a spec that races
// for it would pass by luck on a defect that leaves a whole subject root deaf
// until the connection drops.
var _ = Describe("ordering LISTEN and UNLISTEN on one root", func() {
	var b *Bus

	newBus := func(cfg Config) *Bus {
		GinkgoHelper()
		built, err := New(context.Background(), cfg)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(built.Close)
		return built
	}

	BeforeEach(func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(Migrate(context.Background(), db)).To(Succeed())
		b = newBus(Config{DSN: dsn, DB: db})
	})

	It("does not let a new LISTEN overtake the UNLISTEN it races", func() {
		leaving, err := b.Subscribe("jobs.race.leaving", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())

		unlistenDecided := make(chan struct{})
		release := make(chan struct{})
		arrived := make(chan struct{})
		listenIssued := make(chan struct{}, 1)
		// Set after the first Subscribe, so only the racing pair is observed.
		b.listenBarrier = func(stage, op string) {
			switch {
			case op == "UNLISTEN" && stage == "issue":
				close(unlistenDecided)
				<-release
			case op == "LISTEN" && stage == "enter":
				close(arrived)
			case op == "LISTEN" && stage == "issue":
				listenIssued <- struct{}{}
			}
		}

		unsubscribed := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			unsubscribed <- leaving.Unsubscribe()
		}()
		// The last unsubscribe has decided to UNLISTEN and has not issued it.
		// Eventually rather than a bare receive: a carrier that never reaches
		// the decision at all must fail this spec, not hang it.
		Eventually(unlistenDecided).Should(BeClosed())

		delivered := make(chan []byte, 1)
		subscribed := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := b.Subscribe("jobs.race.arriving", func(d []byte) { delivered <- d })
			subscribed <- err
		}()
		// The competing subscribe has started. Waiting for this rather than for
		// a duration is what makes the assertion below decide something.
		Eventually(arrived).Should(BeClosed())

		// The bite, and it is deterministic in both directions. Unserialized,
		// this LISTEN is on the wire within microseconds of the line above and
		// the UNLISTEN then undoes it. Serialized, it provably cannot be
		// issued while the unsubscribe holds the ordering lock.
		Consistently(listenIssued).ShouldNot(Receive())

		close(release)
		Eventually(unsubscribed).Should(Receive(BeNil()))
		Eventually(subscribed).Should(Receive(BeNil()))

		// The consequence, stated as delivery: the root must not be deaf.
		Expect(b.Publish("jobs.race.arriving", map[string]string{"m": "after"})).To(Succeed())
		Eventually(delivered).Should(Receive(MatchJSON(`{"m":"after"}`)))
	})

	It("listens once for a root and stops listening when its last subscriber leaves", func() {
		// The half of Unsubscribe that the delivery spec cannot see. Without
		// the registration being removed from the map, the second unsubscribe
		// never decides it was the last, and the connection keeps listening on
		// a root nothing is subscribed to for the life of the process.
		issued := make(chan string, 4)
		b.listenBarrier = func(stage, op string) {
			if stage == "issue" {
				issued <- op
			}
		}

		one, err := b.Subscribe("jobs.refcount.a", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		two, err := b.Subscribe("jobs.refcount.b", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())

		Expect(one.Unsubscribe()).To(Succeed())
		Expect(two.Unsubscribe()).To(Succeed())

		Expect(issued).To(HaveLen(2))
		Expect(<-issued).To(Equal("LISTEN"))
		Expect(<-issued).To(Equal("UNLISTEN"))
	})

	It("waits for the resolver as well as the listener when it closes", func() {
		// Close returning while the resolver is still dispatching hands the
		// caller a bus whose handlers can still fire after shutdown, against a
		// database handle the process is about to drop.
		//
		// Asserted as "Close has not returned yet" rather than as "the done
		// channel is closed afterwards": the resolver exits on its own when the
		// context is cancelled, so the second shape passes whether Close waits
		// or not.
		dispatching := make(chan struct{}, 1)
		release := make(chan struct{})
		var releaseOnce sync.Once
		// Registered inside the It, so it runs BEFORE the bus teardown the
		// BeforeEach registered. A failing assertion below must not leave the
		// resolver parked on a channel that Close is waiting for.
		DeferCleanup(func() { releaseOnce.Do(func() { close(release) }) })
		b.listenBarrier = func(stage, op string) {
			if op == "DELIVER" && stage == "enter" {
				select {
				case dispatching <- struct{}{}:
				default:
				}
				<-release
			}
		}
		_, err := b.Subscribe("jobs.close.pending", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(b.Publish("jobs.close.pending", map[string]string{"m": "x"})).To(Succeed())
		Eventually(dispatching).Should(Receive())

		closed := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			b.Close()
			close(closed)
		}()

		Consistently(closed).ShouldNot(BeClosed())
		releaseOnce.Do(func() { close(release) })
		Eventually(closed).Should(BeClosed())
	})

	It("stops the delivery goroutine of a subscription that has left", func() {
		// The other half. A subscription that is deregistered but whose runner
		// is never stopped leaks a goroutine per Unsubscribe, and the delivery
		// spec cannot tell the two halves apart because either one alone makes
		// the handler silent.
		s, err := b.Subscribe("jobs.refcount.runner", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())

		Expect(s.Unsubscribe()).To(Succeed())

		Eventually(s.(*subscription).done).Should(BeClosed())
	})
})
