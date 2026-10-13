package pgbus_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The dispatcher takes notifications in the order they arrived, and a spilled
// one is ready only when its row is read. A read that never ends would hold
// back every subject.
var _ = Describe("a read of a spilled row that does not end", func() {
	It("is dropped and counted, and the broadcasts behind it are delivered", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		bus, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db, FetchTimeout: 500 * time.Millisecond})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)

		other := make(chan []byte, 4)
		_, err = bus.Subscribe("cache.evict", func(data []byte) { other <- data })
		Expect(err).ToNot(HaveOccurred())
		_, err = bus.Subscribe("jobs.stuck.stream", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())

		// The row exists, and a lock stops every SELECT on the table.
		Expect(db.Create(&pgbus.BusMessage{ID: "stuck-1", Subject: "jobs.stuck.stream", Payload: []byte(`{"i":1}`)}).Error).To(Succeed())
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

		Expect(db.Exec("SELECT pg_notify('localai_jobs', ?)", `{"s":"jobs.stuck.stream","i":"stuck-1"}`).Error).To(Succeed())
		Expect(bus.Publish("cache.evict", map[string]string{"model": "m"})).To(Succeed())

		Eventually(other, 5*time.Second).Should(Receive(MatchJSON(`{"model":"m"}`)),
			"one stuck read held back the delivery on every subject")
		Expect(bus.Dropped()).To(Equal(uint64(1)))
	})
})
