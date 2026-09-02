package monitoring

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// collectedMetricNames returns the names of every metric the reader collected,
// so a spec can assert that a gauge is present or absent from a scrape.
func collectedMetricNames(reader sdkmetric.Reader) []string {
	var rm metricdata.ResourceMetrics
	ExpectWithOffset(1, reader.Collect(context.Background(), &rm)).To(Succeed())
	names := []string{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names = append(names, m.Name)
		}
	}
	return names
}

// collectedInt64Gauge returns the single data point of an int64 gauge.
func collectedInt64Gauge(reader sdkmetric.Reader, name string) int64 {
	var rm metricdata.ResourceMetrics
	ExpectWithOffset(1, reader.Collect(context.Background(), &rm)).To(Succeed())
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			ExpectWithOffset(1, ok).To(BeTrue(), "%s is not an int64 gauge", name)
			ExpectWithOffset(1, gauge.DataPoints).To(HaveLen(1))
			return gauge.DataPoints[0].Value
		}
	}
	Fail("gauge "+name+" was not collected", 1)
	return 0
}

// closeDB drops the connection pool, which is the closest a test can get to
// the database being unreachable mid-scrape.
func closeDB(db *gorm.DB) {
	sqlDB, err := db.DB()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, sqlDB.Close()).To(Succeed())
}

// Four wedged transactions pinned the vacuum horizon for 42 days and nothing
// measured it. These are the numbers that would have caught it on day one.
var _ = Describe("control plane database stats", func() {
	var db *gorm.DB

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
	})

	It("samples an idle database without error and reports a small horizon", func() {
		stats, err := SampleControlPlaneDB(context.Background(), db)
		Expect(err).ToNot(HaveOccurred())
		Expect(stats.OldestXminAge).To(BeNumerically(">=", 0))
		Expect(stats.LongestTransactionSeconds).To(BeNumerically("<", 60),
			"an idle test database must not hold a long transaction")
	})

	It("reports a long-running transaction that is holding the horizon open", func() {
		held := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = db.Transaction(func(tx *gorm.DB) error {
				var one int
				_ = tx.Raw("SELECT 1").Scan(&one).Error
				close(held)
				time.Sleep(3 * time.Second)
				return nil
			})
		}()
		<-held
		time.Sleep(1500 * time.Millisecond)

		stats, err := SampleControlPlaneDB(context.Background(), db)
		Expect(err).ToNot(HaveOccurred())
		Expect(stats.LongestTransactionSeconds).To(BeNumerically(">=", 1))
		<-done
	})

	It("reports the dead tuple ratio of a control-plane table that is accumulating dead rows", func() {
		Expect(db.Exec(`CREATE TABLE backend_nodes (id serial PRIMARY KEY, name text)`).Error).To(Succeed())
		Expect(db.Exec(`INSERT INTO backend_nodes (name) SELECT 'node-' || g FROM generate_series(1, 200) g`).Error).To(Succeed())
		Expect(db.Exec(`DELETE FROM backend_nodes WHERE id % 2 = 0`).Error).To(Succeed())

		// PostgreSQL flushes table statistics asynchronously, so poll rather
		// than assume the delete is visible in the catalog straight away.
		Eventually(func() float64 {
			stats, err := SampleControlPlaneDB(context.Background(), db)
			Expect(err).ToNot(HaveOccurred())
			return stats.DeadTupleRatio["backend_nodes"]
		}, 30*time.Second, 500*time.Millisecond).Should(BeNumerically(">", 0),
			"a table with 100 deleted rows must report dead tuples")
	})

	It("fails the sample rather than hanging when the caller's context is already done", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := SampleControlPlaneDB(ctx, db)
		Expect(err).To(HaveOccurred(), "the sample must run under the caller's context")
	})

	It("registers gauges without error", func() {
		Expect(RegisterControlPlaneDBMetrics(db, time.Minute)).To(Succeed())
	})

	Describe("cached sampling", func() {
		It("does not re-read the database inside the minimum interval", func() {
			sampler := &cachedDBSampler{db: db, minInterval: time.Hour}
			first, ok := sampler.stats(context.Background())
			Expect(ok).To(BeTrue())
			Expect(first.LongestTransactionSeconds).To(BeNumerically("<", 1))

			held := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = db.Transaction(func(tx *gorm.DB) error {
					var one int
					_ = tx.Raw("SELECT 1").Scan(&one).Error
					close(held)
					time.Sleep(3 * time.Second)
					return nil
				})
			}()
			<-held
			time.Sleep(1500 * time.Millisecond)

			// A fresh read sees the open transaction, so a cached read that
			// still reports the old value proves the database was not touched.
			fresh, err := SampleControlPlaneDB(context.Background(), db)
			Expect(err).ToNot(HaveOccurred())
			Expect(fresh.LongestTransactionSeconds).To(BeNumerically(">=", 1))

			cached, ok := sampler.stats(context.Background())
			Expect(ok).To(BeTrue())
			Expect(cached.LongestTransactionSeconds).To(Equal(first.LongestTransactionSeconds))
			<-done
		})

		It("keeps reporting the last good values once the database is unreachable", func() {
			Expect(db.Exec(`CREATE TABLE node_models (id serial PRIMARY KEY, name text)`).Error).To(Succeed())
			Expect(db.Exec(`INSERT INTO node_models (name) VALUES ('m1')`).Error).To(Succeed())

			sampler := &cachedDBSampler{db: db, minInterval: 0}
			Eventually(func() bool {
				stats, ok := sampler.stats(context.Background())
				_, seen := stats.DeadTupleRatio["node_models"]
				return ok && seen
			}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
			good, _ := sampler.stats(context.Background())

			closeDB(db)

			// minInterval is zero, so this call does attempt a fresh sample and
			// that sample fails. The last good values must survive it.
			after, ok := sampler.stats(context.Background())
			Expect(ok).To(BeTrue(), "a failed sample must not blank out the gauges")
			Expect(after.OldestXminAge).To(Equal(good.OldestXminAge))
			Expect(after.DeadTupleRatio).To(HaveKey("node_models"))
		})

		It("rate-limits retries against a failing database to one attempt per interval", func() {
			attempts := 0
			// Raw().Scan() runs through gorm's row callback, so this counts one
			// tick per statement the sampler actually sends to the database.
			Expect(db.Callback().Row().After("gorm:row").Register("count_attempts", func(tx *gorm.DB) {
				attempts++
			})).To(Succeed())
			closeDB(db)

			sampler := &cachedDBSampler{db: db, minInterval: time.Hour}
			for i := 0; i < 5; i++ {
				_, ok := sampler.stats(context.Background())
				Expect(ok).To(BeFalse())
			}

			Expect(attempts).To(Equal(1),
				"a failing sample must cost the cache interval too, or a struggling database is retried on every scrape")
		})

		It("reports nothing at all before the first successful sample", func() {
			closeDB(db)

			sampler := &cachedDBSampler{db: db, minInterval: 0}
			stats, ok := sampler.stats(context.Background())
			Expect(ok).To(BeFalse(),
				"a zero xmin age would read as a healthy horizon, so nothing must be reported")
			Expect(stats.OldestXminAge).To(BeZero())
		})
	})

	Describe("scraping the gauges", func() {
		var reader *sdkmetric.ManualReader

		BeforeEach(func() {
			reader = sdkmetric.NewManualReader()
			otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
		})

		It("exports the three gauges on a scrape", func() {
			Expect(db.Exec(`CREATE TABLE backend_nodes (id serial PRIMARY KEY, name text)`).Error).To(Succeed())
			Expect(db.Exec(`INSERT INTO backend_nodes (name) VALUES ('n1')`).Error).To(Succeed())
			Expect(RegisterControlPlaneDBMetrics(db, 0)).To(Succeed())

			Eventually(func() []string {
				return collectedMetricNames(reader)
			}, 30*time.Second, 500*time.Millisecond).Should(ContainElements(
				"localai_control_plane_oldest_xmin_age",
				"localai_control_plane_longest_transaction_seconds",
				"localai_control_plane_dead_tuple_ratio",
			))
		})

		It("serves a scrape from the last good sample while the database is down", func() {
			Expect(RegisterControlPlaneDBMetrics(db, 0)).To(Succeed())
			before := collectedInt64Gauge(reader, "localai_control_plane_oldest_xmin_age")

			closeDB(db)

			Expect(collectedMetricNames(reader)).To(ContainElement("localai_control_plane_oldest_xmin_age"),
				"a scrape during a database outage must not drop the gauges")
			Expect(collectedInt64Gauge(reader, "localai_control_plane_oldest_xmin_age")).To(Equal(before))
		})

		It("omits the gauges from a scrape taken before any sample succeeded", func() {
			closeDB(db)
			Expect(RegisterControlPlaneDBMetrics(db, 0)).To(Succeed())

			Expect(collectedMetricNames(reader)).ToNot(ContainElement("localai_control_plane_oldest_xmin_age"))
		})
	})
})

// The table names the gauges query used to be a hardcoded literal list. That
// compiles forever and matches nothing the moment a model's table name moves,
// and a dead-tuple gauge that matched no rows is indistinguishable from a
// healthy cluster. These specs pin that the names come from gorm instead.
var _ = Describe("control plane table names", func() {
	var db *gorm.DB

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
	})

	It("resolves the registry tables the gauges report on", func() {
		names, err := controlPlaneTableNames(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(names).To(ConsistOf("backend_nodes", "node_models", "gallery_operations"))
	})

	It("honours a TableName override rather than guessing from the type", func() {
		names, err := controlPlaneTableNames(db)
		Expect(err).ToNot(HaveOccurred())

		// GalleryOperationRecord overrides TableName. Naive pluralisation of the
		// type would yield gallery_operation_records, so this assertion fails if
		// the resolution ever stops consulting the model.
		Expect(names).To(ContainElement("gallery_operations"))
		Expect(names).ToNot(ContainElement("gallery_operation_records"))
	})
})
