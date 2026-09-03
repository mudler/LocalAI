// SPDX-License-Identifier: MIT

package pgbus_test

import (
	"context"
	"fmt"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The two failure modes NATS did not have, and both are silent.
//
// Neither is provoked with a double or a clock here. A slow consumer is a real
// SELECT held by a real table lock, and a dropped listener is a real
// pg_terminate_backend against the session's own application_name, because a
// double that never touches the transport cannot fail the way the transport
// fails.
var _ = Describe("the listener under a slow consumer and a dropped connection", func() {
	var (
		db  *gorm.DB
		dsn string
	)

	newBus := func(cfg pgbus.Config) *pgbus.Bus {
		GinkgoHelper()
		cfg.DSN, cfg.DB = dsn, db
		b, err := pgbus.New(context.Background(), cfg)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(b.Close)
		return b
	}

	received := func(b *pgbus.Bus, filter string) <-chan []byte {
		GinkgoHelper()
		out := make(chan []byte, 128)
		s, err := b.Subscribe(filter, func(data []byte) { out <- data })
		Expect(err).ToNot(HaveOccurred())
		Expect(s).ToNot(BeNil())
		return out
	}

	// terminate drops exactly this carrier's LISTEN session and nobody else's.
	// Matching on the application name rather than on "every backend except
	// mine" is what lets a spec assert on a peer bus that stayed up.
	terminate := func(b *pgbus.Bus) {
		GinkgoHelper()
		var killed int64
		Expect(db.Raw(
			"SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE application_name = ?",
			b.ApplicationName(),
		).Scan(&killed).Error).To(Succeed())
		Expect(killed).To(BeNumerically("==", 1), "expected exactly one listener session to drop")
	}

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
	})

	Describe("a resolver that cannot keep up", func() {
		It("keeps draining the connection and drops, rather than stalling on the connection", func() {
			// The hazard this whole split exists for. PostgreSQL holds
			// undelivered notifications in an async queue that is shared by
			// every session on the SERVER, so a listener that stopped draining
			// would not merely fall behind: it can block COMMIT for unrelated
			// publishers, and the backend eventually kills the session, which
			// costs this replica every subsequent broadcast rather than these
			// few.
			//
			// The resolver is stalled through the transport and not through a
			// seam: a spilled broadcast is resolved with a SELECT on
			// bus_messages, and an ACCESS EXCLUSIVE lock held by another
			// transaction stops that SELECT for exactly as long as the spec
			// wants it stopped. Nothing here waits on a clock.
			const queue = 8
			const sent = queue + 64

			b := newBus(pgbus.Config{Queue: queue, Retention: time.Hour})
			out := received(b, "jobs.spill.probe")

			channel, err := pgbus.ChannelFor("jobs.spill.probe")
			Expect(err).ToNot(HaveOccurred())

			// Distinct rows, so every notification carries a distinct payload:
			// PostgreSQL collapses duplicate notifications raised in one
			// transaction, and a spec that relied on identical payloads would
			// be counting on that not happening.
			ids := make([]string, sent)
			for i := range ids {
				ids[i] = fmt.Sprintf("spill-%02d", i)
				Expect(db.Create(&pgbus.BusMessage{
					ID: ids[i], Subject: "jobs.spill.probe", Payload: []byte(fmt.Sprintf(`{"i":%d}`, i)),
				}).Error).To(Succeed())
			}

			held := db.Begin()
			Expect(held.Error).ToNot(HaveOccurred())
			released := false
			release := func() {
				if !released {
					released = true
					Expect(held.Rollback().Error).ToNot(HaveOccurred())
				}
			}
			DeferCleanup(release)
			Expect(held.Exec("LOCK TABLE bus_messages IN ACCESS EXCLUSIVE MODE").Error).To(Succeed())

			for _, id := range ids {
				Expect(db.Exec("SELECT pg_notify(?, ?)", channel,
					fmt.Sprintf(`{"s":"jobs.spill.probe","i":%q}`, id)).Error).To(Succeed())
			}

			// The listener took every notification off the connection while the
			// resolver could not move: the queue filled and the surplus was
			// dropped. A listener that had blocked instead would leave this at
			// zero forever.
			Eventually(b.Dropped, 30*time.Second).Should(BeNumerically(">", 0))
			Expect(b.IsConnected()).To(BeTrue(), "dropping must not cost the connection")

			release()

			// Still a working carrier once the consumer catches up. Bounded
			// loss, not a wedge.
			Eventually(out, 30*time.Second).Should(Receive())
		})

		It("reports nothing dropped on a carrier that is keeping up", func() {
			// The zero the spec above is measured against. Without it a Dropped
			// that returned a constant would satisfy the assertion there.
			b := newBus(pgbus.Config{})
			out := received(b, "jobs.fast.consumer")

			Expect(b.Publish("jobs.fast.consumer", map[string]int{"i": 1})).To(Succeed())

			Eventually(out).Should(Receive())
			Expect(b.Dropped()).To(BeZero())
		})
	})

	Describe("losing the LISTEN connection", func() {
		It("invokes every OnReconnect callback once the channels are listened again", func() {
			// syncstate.SyncedMap syncs deltas only, so this callback is the one
			// mechanism it has to converge after a gap. It reaches it through an
			// optional interface assertion, which means deleting the invocation
			// compiles, passes every other spec, and leaves every adopter
			// silently diverging. This spec is the only guard there is.
			b := newBus(pgbus.Config{})
			_ = received(b, "jobs.reconnect.probe")

			calls := make(chan struct{}, 8)
			b.OnReconnect(func() { calls <- struct{}{} })
			// Not fired by the connection it was registered on: a callback that
			// ran on the first connect would re-hydrate every adopter at boot
			// for no reason.
			Consistently(calls, 200*time.Millisecond).ShouldNot(Receive())

			terminate(b)

			// The callback cannot fire without a lost connection, a redial and a
			// completed re-LISTEN, so its arrival is what proves the whole cycle
			// ran. Sampling IsConnected for the false half would be sampling: the
			// window between the loss and the first redial is sub-millisecond,
			// and a spec that catches it nine times in ten is worse than none.
			Eventually(calls, 30*time.Second).Should(Receive())
			Expect(b.IsConnected()).To(BeTrue())
		})

		It("delivers again on a channel it had to re-listen", func() {
			// Separate from the callback assertion on purpose: a carrier can be
			// reconnected, and report itself connected, and be deaf, because a
			// connection without its LISTEN registrations produces no error
			// anywhere. Deleting the re-LISTEN reddens this and leaves the
			// callback spec above green.
			b := newBus(pgbus.Config{})
			publisher := newBus(pgbus.Config{})
			out := received(b, "jobs.relisten.probe")

			terminate(b)

			Eventually(func() bool {
				// Retried because a NOTIFY that lands between the loss and the
				// re-LISTEN is genuinely gone: this carrier is at-most-once, and
				// what is being asserted is that delivery RESUMES.
				_ = publisher.Publish("jobs.relisten.probe", map[string]string{"m": "after"})
				select {
				case <-out:
					return true
				default:
					return false
				}
			}, 30*time.Second, 200*time.Millisecond).Should(BeTrue())
		})

		It("does not run a reconnect callback on the goroutine that owns the connection", func() {
			// A callback re-hydrates from a durable source, which is a database
			// query. Running one inline would put that query on the path whose
			// only job is to keep PostgreSQL's async queue draining, and a
			// callback that never returned would leave this carrier deaf for
			// good. Deleting the `go` compiles and is invisible everywhere else.
			b := newBus(pgbus.Config{})
			publisher := newBus(pgbus.Config{})
			out := received(b, "jobs.blockedcb.probe")

			entered := make(chan struct{}, 1)
			forever := make(chan struct{})
			DeferCleanup(func() { close(forever) })
			b.OnReconnect(func() {
				entered <- struct{}{}
				<-forever
			})

			terminate(b)

			Eventually(entered, 30*time.Second).Should(Receive())
			Eventually(func() bool {
				_ = publisher.Publish("jobs.blockedcb.probe", map[string]string{"m": "after"})
				select {
				case <-out:
					return true
				default:
					return false
				}
			}, 30*time.Second, 200*time.Millisecond).Should(BeTrue())
		})
	})

	Describe("naming the LISTEN session", func() {
		It("gives each carrier an application_name an operator can find in pg_stat_activity", func() {
			// Without it, dropping one replica's listener means dropping every
			// backend on the database, and an operator asking how many replicas
			// are listening has nothing to count.
			b := newBus(pgbus.Config{})

			var sessions int64
			Expect(db.Raw(
				"SELECT count(*) FROM pg_stat_activity WHERE application_name = ?", b.ApplicationName(),
			).Scan(&sessions).Error).To(Succeed())

			Expect(b.ApplicationName()).To(HavePrefix("localai_pgbus_"))
			Expect(sessions).To(BeNumerically("==", 1))
		})

		It("names two carriers on one database differently", func() {
			first := newBus(pgbus.Config{})
			second := newBus(pgbus.Config{})

			Expect(first.ApplicationName()).ToNot(Equal(second.ApplicationName()))
		})
	})

	Describe("the queue and retention settings", func() {
		It("takes the exported defaults when both are zero", func() {
			b := newBus(pgbus.Config{})

			Expect(b.QueueDepth()).To(Equal(pgbus.DefaultQueueDepth))
			Expect(b.SpillRetention()).To(Equal(pgbus.DefaultSpillRetention))
		})

		It("takes what it was configured with when they are not", func() {
			b := newBus(pgbus.Config{Queue: 7, Retention: 3 * time.Minute})

			Expect(b.QueueDepth()).To(Equal(7))
			Expect(b.SpillRetention()).To(Equal(3 * time.Minute))
		})

		It("runs the purge loop off the retention when no interval is configured", func() {
			// The interval is derived rather than configured in production, so
			// nothing else proves the derivation is wired: a purge loop pinned
			// to a constant would retire rows too, just not on the schedule the
			// retention asks for.
			Expect(db.Create(&pgbus.BusMessage{
				ID: "aged", Subject: "jobs.x", Payload: []byte(`{}`),
			}).Error).To(Succeed())
			Expect(db.Exec(
				"UPDATE bus_messages SET created_at = now() - interval '1 hour' WHERE id = 'aged'",
			).Error).To(Succeed())

			newBus(pgbus.Config{Retention: 40 * time.Millisecond})

			Eventually(func() (int64, error) {
				var n int64
				err := db.Model(&pgbus.BusMessage{}).Count(&n).Error
				return n, err
			}, 30*time.Second).Should(BeZero())
		})
	})

	Describe("what the carrier's own health is allowed to reach", func() {
		It("keeps Dropped and IsConnected off the interface consumers hold", func() {
			// The invariant, guarded structurally. "This replica is behind" and
			// "this replica's listener is down" are facts about a FRONTEND; the
			// conditions a scheduler acts on are all facts about a WORKER. As
			// long as neither method is reachable through messaging.Broadcaster,
			// no consumer can read a carrier failure as a worker's absence
			// without reaching for the concrete type and saying so.
			carrier := reflect.TypeOf((*messaging.Broadcaster)(nil)).Elem()

			_, hasDropped := carrier.MethodByName("Dropped")
			_, hasConnected := carrier.MethodByName("IsConnected")

			Expect(hasDropped).To(BeFalse(), "a subscriber's loss must not be readable as a node's state")
			Expect(hasConnected).To(BeFalse(), "a carrier's link must not be readable as a node's state")
		})
	})
})
