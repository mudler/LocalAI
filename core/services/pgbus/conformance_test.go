package pgbus_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The carrier runs the suite that every fan-out carrier runs, on two ends over
// one database as two replicas have. It serves the broadcast roots only.
var _ = Describe("the PostgreSQL broadcast carrier", func() {
	messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		open := func() *pgbus.Bus {
			b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			return b
		}
		bus, peer := open(), open()
		return messagingtest.Carrier{
			Bus: bus, Peer: peer,
			Cleanup: func() { bus.Close(); peer.Close() },
			Dropped: func() uint64 { return bus.Dropped() + peer.Dropped() },
		}
	})
})
