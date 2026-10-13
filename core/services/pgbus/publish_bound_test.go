package pgbus_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// A publish takes one connection of the pool for as long as its statement runs.
// The pool is unbounded, so a burst of publishers opens as many connections as
// there are publishers, and a stock server refuses the surplus.
var _ = Describe("bounding the connections that publishing uses", func() {
	It("publishes 1000 concurrent broadcasts without a connection error", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		newBus := func() *pgbus.Bus {
			b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db, Queue: 2048, Retention: time.Hour})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(b.Close)
			return b
		}
		pub, sub := newBus(), newBus()

		var got atomic.Int64
		s, err := sub.Subscribe("jobs.bound.stream", func([]byte) { got.Add(1) })
		Expect(err).ToNot(HaveOccurred())

		const total = 1000
		var failures atomic.Int64
		var firstErr atomic.Value
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < total; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				fill := "x"
				if i%10 == 0 {
					fill = strings.Repeat("y", 9*1024) // spills
				}
				<-start
				if err := pub.Publish("jobs.bound.stream", map[string]string{"f": fill}); err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				}
			}(i)
		}
		close(start)
		wg.Wait()

		Expect(failures.Load()).To(BeZero(), "first error: %v", firstErr.Load())
		// Every broadcast is delivered or is in a drop counter. Nothing may go
		// missing without a count.
		accounted := func() int64 {
			return got.Load() + int64(sub.Dropped())
		}
		Eventually(accounted, 60*time.Second).Should(Equal(int64(total)))
		_ = s
	})

	It("returns the error of the context when it waits for a slot that does not free up", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		bus, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db, MaxPublishers: 1})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)

		// A lock on the spill table holds the insert of a spilled publish, and
		// with it the only slot.
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

		first := make(chan error, 1)
		go func() {
			first <- bus.Publish("jobs.slot.stream", map[string]string{"f": strings.Repeat("y", 9*1024)})
		}()
		Eventually(func() int64 {
			var n int64
			Expect(db.Raw(`SELECT count(*) FROM pg_stat_activity
				WHERE wait_event_type = 'Lock' AND query LIKE '%INSERT INTO "bus_messages"%'`).Scan(&n).Error).To(Succeed())
			return n
		}, 10*time.Second, 50*time.Millisecond).Should(Equal(int64(1)))

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		err = bus.PublishContext(ctx, "jobs.slot.stream", map[string]string{"f": "x"})
		Expect(err).To(MatchError(context.DeadlineExceeded))

		release()
		Eventually(first, 10*time.Second).Should(Receive(BeNil()))
	})
})
