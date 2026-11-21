package carrier_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/carrier"
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

// pgbusFloor opens a pgbus fan-out on the database and returns it as the floor
// of a holder.
func pgbusFloor(ctx context.Context, db *gorm.DB, dsn string) (*carrier.Bus, *carrier.Fanout) {
	GinkgoHelper()
	fan, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: db, DSN: dsn})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(fan.Close)
	return fan.Bus(), fan
}

// pgbusHolder is a holder over a pgbus floor and, when nats is set, a primary
// bus over the process's own view of that fake NATS server.
func pgbusHolder(ctx context.Context, db *gorm.DB, dsn string, nats *testutil.FakeBus) (*carrier.Broadcaster, *gatedBus, *carrier.Fanout) {
	GinkgoHelper()
	floor, fan := pgbusFloor(ctx, db, dsn)
	var (
		gate    *gatedBus
		primary *carrier.Bus
	)
	if nats != nil {
		gate = newGatedBus(nats)
		primary = gate.natsBus()
	}
	h, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: floor, Primary: primary})
	Expect(err).ToNot(HaveOccurred())
	return h, gate, fan
}

var _ = Describe("The pgbus fan-out", func() {
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

	Describe("as the floor of the holder", func() {
		It("carries the broadcasts of a frontend that lost NATS to one that has it, once", func() {
			nats := testutil.NewFakeBus()
			a, aNATS, _ := pgbusHolder(ctx, db, dsn, nats)
			b, _, _ := pgbusHolder(ctx, db, dsn, nats)
			got := make(chan []byte, 8)
			_, err := b.Subscribe("staging.j1.result", func(raw []byte) { got <- raw })
			Expect(err).ToNot(HaveOccurred())

			Expect(a.Publish("staging.j1.result", "over-nats")).To(Succeed())
			Eventually(got, 10*time.Second).Should(Receive(Equal([]byte(`"over-nats"`))))

			aNATS.down()
			Expect(a.Unready()).To(MatchError(carrier.ErrBusUnreachable))
			Expect(a.Publish("staging.j1.result", "over-pg")).To(Succeed())
			Eventually(got, 10*time.Second).Should(Receive(Equal([]byte(`"over-pg"`))))

			aNATS.back()
			Expect(a.Unready()).To(Succeed())
			Expect(a.Publish("staging.j1.result", "over-nats-again")).To(Succeed())
			Eventually(got, 10*time.Second).Should(Receive(Equal([]byte(`"over-nats-again"`))))
			Consistently(got, 300*time.Millisecond).ShouldNot(Receive(), "no message twice")
		})

		It("runs the reconnect hooks of the holder when the LISTEN connection is lost and found again", func() {
			holder, _, _ := pgbusHolder(ctx, db, dsn, nil)
			called := make(chan struct{}, 4)
			holder.OnReconnect(func() { called <- struct{}{} })
			_, err := holder.Subscribe("staging.j2.result", func([]byte) {})
			Expect(err).ToNot(HaveOccurred())

			Expect(db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE 'localai_pgbus_%'").Error).To(Succeed())

			Eventually(called, 30*time.Second).Should(Receive())
		})
	})

	// The suite that every carrier passes, run on two holders whose only bus is
	// pgbus on one database.
	Describe("Broadcaster conformance on holders over pgbus", func() {
		messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
			pgDB, pgDSN := testutil.SetupTestDBWithDSN()
			h1, _, f1 := pgbusHolder(context.Background(), pgDB, pgDSN, nil)
			h2, _, f2 := pgbusHolder(context.Background(), pgDB, pgDSN, nil)
			return messagingtest.Carrier{
				Bus: h1, Peer: h2,
				Dropped: func() uint64 { return f1.Dropped() + f2.Dropped() },
			}
		})
	})
})
