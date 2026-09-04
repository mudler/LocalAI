// SPDX-License-Identifier: MIT

package pgbus_test

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Every delivery spec here builds TWO buses on TWO connections and asserts on
// the one that did NOT publish. One bus that hears itself proves nothing about
// a carrier whose whole job is to reach the OTHER replica, and an in-memory
// double proves less than that: it cannot fail the way a LISTEN connection
// fails.
var _ = Describe("the PostgreSQL broadcast carrier", func() {
	var (
		db       *gorm.DB
		dsn      string
		pub, sub *pgbus.Bus
	)

	// newBus is the only construction path in this file, so a spec cannot
	// accidentally skip the migration and read a missing-table error as a
	// delivery failure.
	newBus := func() *pgbus.Bus {
		GinkgoHelper()
		b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(b.Close)
		return b
	}

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		pub = newBus()
		sub = newBus()
	})

	// received registers a subscriber that forwards every delivery onto a
	// channel, so specs synchronize on delivery rather than on the clock.
	received := func(b *pgbus.Bus, filter string) (<-chan []byte, messaging.Subscription) {
		GinkgoHelper()
		out := make(chan []byte, 16)
		s, err := b.Subscribe(filter, func(data []byte) { out <- data })
		Expect(err).ToNot(HaveOccurred())
		Expect(s).ToNot(BeNil())
		return out, s
	}

	Describe("delivery across two connections", func() {
		It("carries a message to a subscriber on the other bus", func() {
			out, _ := received(sub, "jobs.abc.progress")

			Expect(pub.Publish("jobs.abc.progress", map[string]string{"phase": "downloading"})).To(Succeed())

			Eventually(out).Should(Receive(MatchJSON(`{"phase":"downloading"}`)))
		})

		// The root set is not a formality: a subject family that maps to its own
		// channel is a family a subscriber on a different channel never hears.
		// M1 (Publish hardcoding one channel) and M2 (Subscribe hardcoding one)
		// both present here as silence.
		It("carries a message on a root other than jobs", func() {
			out, _ := received(sub, "gallery.install.progress")

			Expect(pub.Publish("gallery.install.progress", map[string]int{"done": 3})).To(Succeed())

			Eventually(out).Should(Receive(MatchJSON(`{"done":3}`)))
		})

		It("delivers to every subscriber of a subject, on the publishing bus and on the other one", func() {
			// Fan-out is the whole point of this carrier: a replica that
			// published must still run its own handlers, because in production
			// the publisher is one of the replicas that has to react.
			here, _ := received(pub, "cache.evict")
			there, _ := received(sub, "cache.evict")

			Expect(pub.Publish("cache.evict", map[string]string{"model": "m"})).To(Succeed())

			Eventually(here).Should(Receive())
			Eventually(there).Should(Receive())
		})
	})

	Describe("which subjects reach which filters", func() {
		// The expectations are COMPUTED by messaging.SubjectMatches rather than
		// written out, so the carrier and the matcher cannot drift apart: if
		// the carrier ever answers differently from the shared function, this
		// table reddens without anyone having to notice the divergence.
		//
		// Ordering carries the negative half. Every case publishes the probe
		// subject and then a sentinel on a subject the filter certainly
		// matches; a filter that must NOT see the probe is proved by the
		// sentinel arriving first, which needs no clock.
		DescribeTable("agrees with messaging.SubjectMatches",
			func(filter, subject string) {
				out, _ := received(sub, filter)
				sentinel := "jobs.sentinel.progress"
				if !messaging.SubjectMatches(filter, sentinel) {
					sentinel = filter
				}

				Expect(pub.Publish(subject, map[string]string{"m": "probe"})).To(Succeed())
				Expect(pub.Publish(sentinel, map[string]string{"m": "sentinel"})).To(Succeed())

				var first []byte
				Eventually(out).Should(Receive(&first))
				want := `{"m":"sentinel"}`
				if messaging.SubjectMatches(filter, subject) {
					want = `{"m":"probe"}`
				}
				Expect(first).To(MatchJSON(want))
			},
			Entry("exact subject", "jobs.abc.progress", "jobs.abc.progress"),
			Entry("single-token wildcard in the middle", "jobs.*.progress", "jobs.abc.progress"),
			Entry("a sibling subject of the same family", "jobs.*.progress", "jobs.abc.result"),
			Entry("more tokens than the filter has", "jobs.*.progress", "jobs.abc.extra.progress"),
			Entry("fewer tokens than the filter has", "jobs.*.progress", "jobs.progress"),
			Entry("a wildcard in the leading token", "jobs.*", "jobs.abc"),
		)
	})

	Describe("subjects this carrier refuses", func() {
		It("refuses a publish whose root it does not serve, and names the subject", func() {
			err := pub.Publish("weather.today", map[string]string{"sky": "blue"})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("weather.today"))
		})

		It("delivers nothing at all for a refused publish", func() {
			// The refusal has to be a refusal, not an error returned beside a
			// message that went somewhere anyway.
			out, _ := received(sub, "jobs.abc.progress")

			Expect(pub.Publish("weather.today", map[string]string{"sky": "blue"})).ToNot(Succeed())
			Expect(pub.Publish("jobs.abc.progress", map[string]string{"m": "sentinel"})).To(Succeed())

			var first []byte
			Eventually(out).Should(Receive(&first))
			Expect(first).To(MatchJSON(`{"m":"sentinel"}`))
		})

		It("refuses a subscribe whose root it does not serve, and registers nothing", func() {
			s, err := sub.Subscribe("weather.today", func([]byte) {})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("weather.today"))
			Expect(s).To(BeNil())
		})

		It("refuses a filter the shared matcher cannot act on, with that package's error", func() {
			// errors.Is rather than a string: this is what proves the carrier
			// asks messaging.ValidFilter instead of re-spelling the rule.
			s, err := sub.Subscribe("jobs.>", func([]byte) {})

			Expect(errors.Is(err, messaging.ErrUnsupportedFilter)).To(BeTrue(), "got %v", err)
			Expect(s).To(BeNil())
		})

		It("refuses an empty-token filter for the same reason", func() {
			s, err := sub.Subscribe("jobs..progress", func([]byte) {})

			Expect(errors.Is(err, messaging.ErrUnsupportedFilter)).To(BeTrue(), "got %v", err)
			Expect(s).To(BeNil())
		})
	})

	Describe("unsubscribing", func() {
		It("stops delivery to that handler and leaves the others alone", func() {
			gone, s := received(sub, "jobs.abc.progress")
			stays, _ := received(sub, "jobs.abc.progress")

			Expect(s.Unsubscribe()).To(Succeed())
			Expect(pub.Publish("jobs.abc.progress", map[string]string{"m": "after"})).To(Succeed())

			Eventually(stays).Should(Receive(MatchJSON(`{"m":"after"}`)))
			Consistently(gone).ShouldNot(Receive())
		})
	})

	Describe("subscriptions that come and go", func() {
		// The accumulation assertion, and it is deliberately NOT a delivery
		// one. Two subscriptions in this deployment are opened and closed per
		// HTTP REQUEST, so a filter that outlives its Unsubscribe is a replica
		// that gets steadily slower and never fails. Delivery specs cannot see
		// it: the leaked handlers deliver correctly, there are just more of
		// them every request.
		It("registers nothing that outlives its Unsubscribe", func() {
			delivered := make(chan []byte, 8)
			survivor, err := sub.Subscribe("jobs.churn.progress", func(data []byte) { delivered <- data })
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = survivor.Unsubscribe() })

			start := sub.Subscribers()
			Expect(start).To(Equal(1))

			for i := 0; i < 1000; i++ {
				s, err := sub.Subscribe("jobs.churn.progress", func([]byte) {})
				Expect(err).ToNot(HaveOccurred())
				Expect(s.Unsubscribe()).To(Succeed())
			}

			Expect(sub.Subscribers()).To(Equal(start),
				"a subscription that is opened and closed must leave the register where it found it")

			// And the survivor is still exactly one subscriber, not zero and
			// not a thousand. A counter rather than a boolean, because
			// "something arrived" is true for every wrong answer here.
			Expect(pub.Publish("jobs.churn.progress", map[string]string{"m": "after"})).To(Succeed())
			Eventually(delivered).Should(Receive(MatchJSON(`{"m":"after"}`)))
			Consistently(delivered, "500ms", "50ms").ShouldNot(Receive(),
				"a leaked handler would deliver the same notification again")
		})

		It("keeps the channel listened while a subscriber remains", func() {
			// The churn above cycles LISTEN and UNLISTEN a thousand times on a
			// channel that still has a live subscriber. If the refcount and the
			// UNLISTEN it implies were not decided as one step, the channel
			// ends up unlistened with a live subscription on it, and it does
			// not self-heal: the next Subscribe sees first == false and never
			// re-LISTENs.
			out, _ := received(sub, "jobs.abc.progress")
			for i := 0; i < 50; i++ {
				s, err := sub.Subscribe("jobs.other.progress", func([]byte) {})
				Expect(err).ToNot(HaveOccurred())
				Expect(s.Unsubscribe()).To(Succeed())
			}

			Expect(pub.Publish("jobs.abc.progress", map[string]string{"m": "still here"})).To(Succeed())
			Eventually(out).Should(Receive(MatchJSON(`{"m":"still here"}`)))
		})
	})

	Describe("what Subscribe has finished doing when it returns", func() {
		// The per-request subscriptions are why this matters. An SSE client
		// connects and then triggers the work; if Subscribe returns before the
		// LISTEN is on the wire, the first events go nowhere and the stream
		// looks hung with no error on either side.
		//
		// There is deliberately no Eventually around the PUBLISH. Retrying the
		// publish is exactly the thing that would hide an asynchronous LISTEN.
		It("has already issued the LISTEN, every time", func() {
			for i := 0; i < 50; i++ {
				out := make(chan []byte, 1)
				s, err := sub.Subscribe("jobs.race.progress", func(data []byte) { out <- data })
				Expect(err).ToNot(HaveOccurred())

				Expect(pub.Publish("jobs.race.progress", map[string]int{"i": i})).To(Succeed())

				Eventually(out, "10s").Should(Receive(), "iteration %d subscribed and then missed its own first message", i)
				Expect(s.Unsubscribe()).To(Succeed())
			}
		})
	})

	Describe("a handler that does not return", func() {
		It("does not stop delivery to the other subscribers", func() {
			// A carrier whose handlers run on one goroutine is a carrier one
			// blocked SSE writer can wedge for the whole deployment.
			block := make(chan struct{})
			DeferCleanup(func() { close(block) })
			_, _ = sub.Subscribe("jobs.abc.progress", func([]byte) { <-block })
			free, _ := received(sub, "jobs.abc.progress")

			Expect(pub.Publish("jobs.abc.progress", map[string]string{"m": "one"})).To(Succeed())
			Expect(pub.Publish("jobs.abc.progress", map[string]string{"m": "two"})).To(Succeed())

			Eventually(free).Should(Receive(MatchJSON(`{"m":"one"}`)))
			Eventually(free).Should(Receive(MatchJSON(`{"m":"two"}`)))
		})
	})

	Describe("a subscriber that falls behind", func() {
		It("counts what it lost, so the subscriber can go and read the row", func() {
			// The drop is loud in the log of the replica that took it, and that
			// is the wrong party: the subscriber is the one that has to decide
			// to go and read the table instead. Task 12 moves jobs.*.result
			// onto this carrier, and a result has no successor message, so an
			// invisible loss there is a job whose answer silently never
			// arrives.
			block := make(chan struct{})
			DeferCleanup(func() { close(block) })
			s, err := sub.Subscribe("jobs.slow.consumer", func([]byte) { <-block })
			Expect(err).ToNot(HaveOccurred())

			counter, ok := s.(pgbus.DropCounter)
			Expect(ok).To(BeTrue(), "a subscription must be able to report its losses")
			Expect(counter.Dropped()).To(BeZero())

			// Comfortably past the queue depth, stated absolutely rather than
			// as deliveryQueueDepth+n, so a change to the depth moves the
			// behaviour and not the expectation.
			for i := 0; i < 400; i++ {
				Expect(pub.Publish("jobs.slow.consumer", map[string]int{"i": i})).To(Succeed())
			}

			Eventually(counter.Dropped, 30*time.Second).Should(BeNumerically(">", 0))
		})
	})

	Describe("losing the LISTEN connection", func() {
		It("reconnects, restores its registrations and delivers again", func() {
			// The transport failure a fake cannot produce. Terminating the
			// backend sessions drops both buses' pinned connections; what must
			// survive is the SUBSCRIPTION, because a carrier that comes back
			// deaf looks exactly like a peer that stopped publishing.
			out, _ := received(sub, "jobs.abc.progress")

			Expect(db.Exec(
				"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()",
			).Error).To(Succeed())

			Eventually(sub.IsConnected, 30*time.Second, 100*time.Millisecond).Should(BeTrue())
			Eventually(func() bool {
				// Retried because the publishing side's pool is recovering too,
				// and because a NOTIFY that lands between the drop and the
				// re-LISTEN is genuinely lost: this carrier is at-most-once.
				_ = pub.Publish("jobs.abc.progress", map[string]string{"m": "after"})
				select {
				case <-out:
					return true
				default:
					return false
				}
			}, 30*time.Second, 200*time.Millisecond).Should(BeTrue())
		})
	})
})

var _ = Describe("mapping a subject onto a LISTEN channel", func() {
	It("puts a subject on the channel of its first token", func() {
		Expect(pgbus.ChannelFor("jobs.abc.progress")).To(Equal("localai_jobs"))
	})

	It("refuses a subject whose root it does not serve", func() {
		_, err := pgbus.ChannelFor("weather.today")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("weather.today"))
	})

	It("refuses an empty subject", func() {
		_, err := pgbus.ChannelFor("")
		Expect(err).To(HaveOccurred())
	})

	It("keeps every channel it can ever return inside PostgreSQL's identifier limit", func() {
		// Asserted over the whole root set rather than over a sample, so a root
		// added later cannot push a channel past 63 bytes silently. Past that
		// limit PostgreSQL truncates the name instead of failing, which merges
		// two families onto one channel.
		roots := pgbus.BroadcastRoots()
		Expect(roots).ToNot(BeEmpty())
		for _, root := range roots {
			channel, err := pgbus.ChannelFor(root + ".anything")
			Expect(err).ToNot(HaveOccurred(), root)
			Expect(len(channel)).To(BeNumerically("<=", 63), channel)
		}
	})
})

var _ = Describe("constructing the carrier", func() {
	It("refuses a non-PostgreSQL handle and names the dialect", func() {
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "bus.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())

		_, err = pgbus.New(context.Background(), pgbus.Config{DSN: "postgres://x/y", DB: db})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("sqlite"))
	})

	It("refuses a DSN that points at a different database from the pool", func() {
		// The failure this excludes has no other symptom: every publish
		// succeeds, every subscribe succeeds, and nothing is ever delivered.
		db, _ := testutil.SetupTestDBWithDSN()
		_, otherDSN := testutil.SetupTestDBWithDSN()

		_, err := pgbus.New(context.Background(), pgbus.Config{DSN: otherDSN, DB: db})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("different database"))
	})

	It("reports itself connected while open and disconnected once closed", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())

		b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
		Expect(err).ToNot(HaveOccurred())
		Expect(b.IsConnected()).To(BeTrue())
		Expect(b.DSN()).To(Equal(dsn))

		b.Close()
		Expect(b.IsConnected()).To(BeFalse())
	})

	It("survives a second Close", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
		Expect(err).ToNot(HaveOccurred())

		b.Close()
		Expect(b.Close).ToNot(Panic())
	})

	It("refuses an empty DSN rather than opening a carrier that cannot listen", func() {
		db, _ := testutil.SetupTestDBWithDSN()

		_, err := pgbus.New(context.Background(), pgbus.Config{DB: db})

		Expect(err).To(HaveOccurred())
	})

	It("refuses a nil handle", func() {
		_, err := pgbus.New(context.Background(), pgbus.Config{DSN: "postgres://x/y"})
		Expect(err).To(HaveOccurred())
	})
})
