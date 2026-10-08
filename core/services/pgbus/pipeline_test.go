package pgbus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// A spilled broadcast costs a SELECT on every replica. The replica reads several
// rows at the same time, and delivers in the order of arrival.
var _ = Describe("reading spilled broadcasts", func() {
	var (
		db       *gorm.DB
		pub, sub *pgbus.Bus
	)

	BeforeEach(func() {
		var dsn string
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		for _, b := range []**pgbus.Bus{&pub, &sub} {
			built, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db, Queue: 2048, Retention: time.Hour})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(built.Close)
			*b = built
		}
	})

	type probe struct {
		I    int    `json:"i"`
		Fill string `json:"f"`
	}

	It("delivers inline and spilled broadcasts of one subject in the order they were published", func() {
		var mu sync.Mutex
		var got []int
		_, err := sub.Subscribe("jobs.order.stream", func(data []byte) {
			var p probe
			Expect(json.Unmarshal(data, &p)).To(Succeed())
			mu.Lock()
			got = append(got, p.I)
			mu.Unlock()
		})
		Expect(err).ToNot(HaveOccurred())

		const total = 300
		for i := 0; i < total; i++ {
			fill := "x"
			if i%3 == 0 {
				fill = strings.Repeat("y", 16*1024) // spills
			}
			Expect(pub.Publish("jobs.order.stream", probe{I: i, Fill: fill})).To(Succeed())
		}

		Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(got) }, 60*time.Second).Should(Equal(total))
		for i := range got {
			Expect(got[i]).To(Equal(i), "message %d arrived at position %d", got[i], i)
		}
		Expect(sub.Dropped()).To(BeZero())
	})

	// The limit that was measured on a four-core database: a replica with one
	// reader delivered 1,860 of 4,000 broadcasts of 64 KiB sent by 16
	// publishers, and with several readers it delivered all of them. The burst
	// here is smaller, so that it holds on a busy machine too.
	It("delivers a burst of 64 KiB broadcasts from many publishers without a loss", func() {
		var got atomic.Int64
		_, err := sub.Subscribe("jobs.burst.big", func([]byte) { got.Add(1) })
		Expect(err).ToNot(HaveOccurred())

		const total = 1000
		payload := probe{Fill: strings.Repeat("a", 64*1024)}
		var sent atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < 16; w++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for sent.Add(1) <= total {
					Expect(pub.Publish("jobs.burst.big", payload)).To(Succeed())
				}
			}()
		}
		wg.Wait()

		Eventually(got.Load, 60*time.Second).Should(Equal(int64(total)))
		Expect(sub.Dropped()).To(BeZero())
	})

	It("reads more than one spilled row at the same time", func() {
		out := make(chan int, 64)
		_, err := sub.Subscribe("jobs.overlap.probe", func(data []byte) {
			var p probe
			Expect(json.Unmarshal(data, &p)).To(Succeed())
			out <- p.I
		})
		Expect(err).ToNot(HaveOccurred())

		const total = 8
		for i := 0; i < total; i++ {
			Expect(db.Create(&pgbus.BusMessage{
				ID: fmt.Sprintf("ov-%d", i), Subject: "jobs.overlap.probe", Payload: []byte(fmt.Sprintf(`{"i":%d}`, i)),
			}).Error).To(Succeed())
		}
		// A lock that stops every SELECT on the table. The statements that wait
		// for it are the readers, and a replica with one reader has one.
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
		for i := 0; i < total; i++ {
			Expect(db.Exec("SELECT pg_notify('localai_jobs', ?)",
				fmt.Sprintf(`{"s":"jobs.overlap.probe","i":"ov-%d"}`, i)).Error).To(Succeed())
		}

		waiting := func() int64 {
			var n int64
			Expect(db.Raw(`SELECT count(*) FROM pg_stat_activity
				WHERE wait_event_type = 'Lock' AND query LIKE '%FROM "bus_messages"%'`).Scan(&n).Error).To(Succeed())
			return n
		}
		Eventually(waiting, 30*time.Second, 50*time.Millisecond).Should(BeNumerically(">", 1),
			"the replica reads its spilled rows one after the other")
		release()

		for want := 0; want < total; want++ {
			Eventually(out, 30*time.Second).Should(Receive(Equal(want)))
		}
	})
})
