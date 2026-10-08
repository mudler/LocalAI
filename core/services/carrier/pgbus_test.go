package carrier_test

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// listeners counts the LISTEN sessions that the pgbus carrier holds on the
// database.
func listeners(db *gorm.DB) int64 {
	GinkgoHelper()
	var n int64
	Expect(db.Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'localai_pgbus_%'").Scan(&n).Error).To(Succeed())
	return n
}

// pgbusSet is a set of the tunnel carrier with the pgbus fan-out. The other
// members are fakes: later changes provide them.
func pgbusSet(ctx context.Context, db *gorm.DB, dsn string, epoch int64) (*carrier.Set, *carrier.Fanout) {
	GinkgoHelper()
	fan, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: db, DSN: dsn})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(fan.Close)
	set := newFakeCarrier(cluster.CarrierTunnel, epoch).set
	set.Broadcaster = fan.Broadcaster
	set.OnReconnect = fan.OnReconnect
	set.Close = fan.Close
	Expect(set.Validate()).To(Succeed())
	return set, fan
}

var _ = Describe("The pgbus fan-out of the tunnel carrier", func() {
	var (
		db  *gorm.DB
		dsn string
		ctx context.Context
	)

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		ctx = context.Background()
	})

	It("opens no connection until it is built, and none after it is closed", func() {
		// A deployment on NATS builds nothing, so it holds no LISTEN session.
		Expect(listeners(db)).To(BeZero())

		fan, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: db, DSN: dsn})
		Expect(err).ToNot(HaveOccurred())
		Expect(listeners(db)).To(Equal(int64(1)))

		fan.Close()
		Eventually(func() int64 { return listeners(db) }, 10*time.Second).Should(BeZero())
	})

	It("creates the spill table when it is built", func() {
		fan, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: db, DSN: dsn})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(fan.Close)

		Expect(db.Migrator().HasTable("bus_messages")).To(BeTrue())
	})

	It("refuses to be built without a database string, and holds no session then", func() {
		_, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: db})
		Expect(err).To(HaveOccurred())
		Expect(listeners(db)).To(BeZero())
	})

	It("builds a valid set of the tunnel carrier", func() {
		set, _ := pgbusSet(ctx, db, dsn, 4)
		Expect(set.Name).To(Equal(cluster.CarrierTunnel))
		Expect(set.Epoch).To(Equal(int64(4)))
	})

	Describe("behind the holder", func() {
		It("receives, after a swap, what a peer publishes, and keeps what was subscribed before", func() {
			// Two replicas. Each starts on its own fake NATS carrier, with a
			// subscription made before the swap. Each swaps to a pgbus set on
			// the same database, in the order the switch uses.
			type replica struct {
				holder *carrier.Broadcaster
				cur    *atomic.Pointer[carrier.Set]
				first  *carrier.Set
				next   *carrier.Set
				got    chan []byte
			}
			build := func() *replica {
				r := &replica{cur: &atomic.Pointer[carrier.Set]{}, got: make(chan []byte, 8)}
				r.first = newFakeCarrier(cluster.CarrierNATS, 1).set
				r.cur.Store(r.first)
				r.holder = carrier.NewBroadcaster(r.cur)
				_, err := r.holder.Subscribe("jobs.j1.result", func(b []byte) { r.got <- b })
				Expect(err).ToNot(HaveOccurred())
				r.next, _ = pgbusSet(ctx, db, dsn, 2)
				return r
			}
			a, b := build(), build()

			// prepare: both listen on both carriers.
			Expect(a.holder.Listen(a.next)).To(Succeed())
			Expect(b.holder.Listen(b.next)).To(Succeed())
			// commit: a publishes on the new carrier, b has not seen it yet.
			a.cur.Store(a.next)
			Expect(a.holder.Publish("jobs.j1.result", "from-a")).To(Succeed())
			Eventually(b.got, 10*time.Second).Should(Receive(Equal([]byte(`"from-a"`))))
			Eventually(a.got, 10*time.Second).Should(Receive(Equal([]byte(`"from-a"`))))

			// b flips and publishes on the new carrier too. a still hears it.
			b.cur.Store(b.next)
			Expect(b.holder.Publish("jobs.j1.result", "from-b")).To(Succeed())
			Eventually(a.got, 10*time.Second).Should(Receive(Equal([]byte(`"from-b"`))))

			// stable: the old carriers are released and the subscription lives on.
			Expect(a.holder.Release(a.first)).To(Succeed())
			Expect(b.holder.Release(b.first)).To(Succeed())
			Expect(a.holder.Publish("jobs.j1.result", "after")).To(Succeed())
			Eventually(b.got, 10*time.Second).Should(Receive(Equal([]byte(`"after"`))))
		})

		It("runs the reconnect hooks of the holder when the LISTEN connection is lost and found again", func() {
			cur := &atomic.Pointer[carrier.Set]{}
			set, _ := pgbusSet(ctx, db, dsn, 1)
			cur.Store(set)
			holder := carrier.NewBroadcaster(cur)
			called := make(chan struct{}, 4)
			holder.OnReconnect(func() { called <- struct{}{} })
			_, err := holder.Subscribe("jobs.j2.result", func([]byte) {})
			Expect(err).ToNot(HaveOccurred())

			Expect(db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE 'localai_pgbus_%'").Error).To(Succeed())

			Eventually(called, 30*time.Second).Should(Receive())
		})
	})

	// The suite that every carrier passes, run on holders that were swapped from
	// a NATS set to a pgbus set, over two replicas.
	Describe("Broadcaster conformance on holders swapped to pgbus", func() {
		messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
			pgDB, pgDSN := testutil.SetupTestDBWithDSN()
			swapped := func() (*carrier.Broadcaster, *carrier.Fanout) {
				var cur atomic.Pointer[carrier.Set]
				first := newFakeCarrier(cluster.CarrierNATS, 1).set
				cur.Store(first)
				h := carrier.NewBroadcaster(&cur)
				next, fan := pgbusSet(context.Background(), pgDB, pgDSN, 2)
				Expect(h.Listen(next)).To(Succeed())
				cur.Store(next)
				Expect(h.Release(first)).To(Succeed())
				return h, fan
			}
			h1, f1 := swapped()
			h2, f2 := swapped()
			return messagingtest.Carrier{
				Bus: h1, Peer: h2,
				Dropped: func() uint64 { return f1.Dropped() + f2.Dropped() },
			}
		})
	})
})

var _ = Describe("The probe of the LISTEN connection", func() {
	It("succeeds against a database that accepts LISTEN, and leaves no session behind", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(carrier.ProbeListen(context.Background(), dsn)).To(Succeed())
		Eventually(func() int64 { return listeners(db) }, "5s").Should(BeZero())
	})

	It("says why it cannot connect", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := carrier.ProbeListen(ctx, "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
		Expect(err).To(HaveOccurred())
	})

	It("refuses an empty connection string with a reason that names the cause", func() {
		Expect(carrier.ProbeListen(context.Background(), "")).To(MatchError(ContainSubstring("connection string")))
	})
})
