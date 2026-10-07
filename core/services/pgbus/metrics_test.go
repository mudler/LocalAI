package pgbus_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// A broadcast is at-most-once, so the counters are the only place where a loss is
// visible to an operator. Each spec makes the loss happen on a real database and
// reads the counter back through the OpenTelemetry SDK.
var _ = Describe("the counters of the PostgreSQL broadcast carrier", func() {
	var (
		db     *gorm.DB
		dsn    string
		reader *sdkmetric.ManualReader
	)

	newBus := func(cfg pgbus.Config) *pgbus.Bus {
		GinkgoHelper()
		cfg.DSN, cfg.DB = dsn, db
		cfg.Meter = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("pgbus-spec")
		b, err := pgbus.New(context.Background(), cfg)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(b.Close)
		return b
	}

	// counter returns the sum of the counter with the given name over the data
	// points that carry the attribute. An empty key sums them all.
	counter := func(name, key, value string) int64 {
		GinkgoHelper()
		var rm metricdata.ResourceMetrics
		Expect(reader.Collect(context.Background(), &rm)).To(Succeed())
		var sum int64
		for _, scope := range rm.ScopeMetrics {
			for _, m := range scope.Metrics {
				data, ok := m.Data.(metricdata.Sum[int64])
				if m.Name != name || !ok {
					continue
				}
				for _, p := range data.DataPoints {
					if key == "" {
						sum += p.Value
						continue
					}
					if v, found := p.Attributes.Value(attribute.Key(key)); found && v.AsString() == value {
						sum += p.Value
					}
				}
			}
		}
		return sum
	}

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		reader = sdkmetric.NewManualReader()
	})

	It("counts a broadcast by the path that it took", func() {
		b := newBus(pgbus.Config{})

		Expect(b.Publish("jobs.m1.progress", map[string]string{"k": "small"})).To(Succeed())
		Expect(b.Publish("jobs.m1.progress", map[string]string{"k": "small"})).To(Succeed())
		Expect(b.Publish("jobs.m1.result", map[string]string{"k": strings.Repeat("a", 64*1024)})).To(Succeed())

		Expect(counter("localai_pgbus_published_total", "path", "inline")).To(Equal(int64(2)))
		Expect(counter("localai_pgbus_published_total", "path", "spill")).To(Equal(int64(1)))
	})

	It("does not count a broadcast that was refused", func() {
		b := newBus(pgbus.Config{})

		Expect(b.Publish("nodes.n1.backend.stop", "x")).ToNot(Succeed())

		Expect(counter("localai_pgbus_published_total", "", "")).To(BeZero())
	})

	It("counts a notification that the listener had to drop, by stage", func() {
		b := newBus(pgbus.Config{Queue: 4, Retention: time.Hour})
		out := make(chan []byte, 128)
		_, err := b.Subscribe("jobs.m2.probe", func(data []byte) { out <- data })
		Expect(err).ToNot(HaveOccurred())

		const sent = 40
		for i := 0; i < sent; i++ {
			Expect(db.Create(&pgbus.BusMessage{
				ID: fmt.Sprintf("m2-%02d", i), Subject: "jobs.m2.probe", Payload: []byte(`{}`),
			}).Error).To(Succeed())
		}
		// A lock that stops the SELECT of the resolver, so that the queue fills.
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
		for i := 0; i < sent; i++ {
			Expect(db.Exec("SELECT pg_notify('localai_jobs', ?)",
				fmt.Sprintf(`{"s":"jobs.m2.probe","i":"m2-%02d"}`, i)).Error).To(Succeed())
		}

		Eventually(func() int64 { return counter("localai_pgbus_dropped_total", "stage", "listener") },
			30*time.Second).Should(BeNumerically(">", 0))
		Expect(int64(b.Dropped())).To(BeNumerically(">=", counter("localai_pgbus_dropped_total", "stage", "listener")))
		release()
	})

	It("counts a notification that could not be resolved, by stage", func() {
		b := newBus(pgbus.Config{})
		out := make(chan []byte, 4)
		_, err := b.Subscribe("jobs.m3.probe", func(data []byte) { out <- data })
		Expect(err).ToNot(HaveOccurred())

		Expect(db.Exec("SELECT pg_notify('localai_jobs', ?)", `{"s":"jobs.m3.probe","i":"no-such-row"}`).Error).To(Succeed())
		Expect(db.Exec("SELECT pg_notify('localai_jobs', ?)", `not json`).Error).To(Succeed())

		Eventually(func() int64 { return counter("localai_pgbus_dropped_total", "stage", "resolve") },
			10*time.Second).Should(Equal(int64(2)))
		Expect(out).ToNot(Receive())
	})

	It("counts a message that a slow subscriber lost, by stage", func() {
		b := newBus(pgbus.Config{})
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		_, err := b.Subscribe("jobs.m4.slow", func([]byte) { <-block })
		Expect(err).ToNot(HaveOccurred())

		for i := 0; i < 400; i++ {
			Expect(b.Publish("jobs.m4.slow", map[string]int{"i": i})).To(Succeed())
		}

		Eventually(func() int64 { return counter("localai_pgbus_dropped_total", "stage", "subscription") },
			30*time.Second).Should(BeNumerically(">", 0))
	})

	It("counts the spilled rows that the purge deleted", func() {
		b := newBus(pgbus.Config{Retention: time.Hour})
		for _, id := range []string{"p1", "p2", "p3"} {
			Expect(db.Create(&pgbus.BusMessage{ID: id, Subject: "jobs.x", Payload: []byte(`{}`)}).Error).To(Succeed())
		}
		Expect(db.Exec("UPDATE bus_messages SET created_at = now() - interval '2 hours'").Error).To(Succeed())

		deleted, err := b.PurgeBefore(context.Background(), time.Hour)

		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(Equal(int64(3)))
		Expect(counter("localai_pgbus_spill_purged_total", "", "")).To(Equal(int64(3)))
	})

	It("counts a reconnection of the LISTEN connection", func() {
		b := newBus(pgbus.Config{})
		_, err := b.Subscribe("jobs.m5.probe", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(counter("localai_pgbus_reconnections_total", "", "")).To(BeZero())

		Expect(db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = ?", b.ApplicationName()).Error).To(Succeed())

		Eventually(func() int64 { return counter("localai_pgbus_reconnections_total", "", "") },
			30*time.Second, 100*time.Millisecond).Should(Equal(int64(1)))
	})
})
