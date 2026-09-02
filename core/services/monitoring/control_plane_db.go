package monitoring

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"
)

// controlPlaneTables are the registry tables whose bloat degrades routing.
var controlPlaneTables = []string{"backend_nodes", "node_models", "gallery_operations"}

// controlPlaneSampleTimeout bounds one catalog sample. A scrape drives the
// collection, so an unbounded query on a wedged database would hold the
// Prometheus scrape open for as long as the database stays wedged, which is
// precisely the situation these gauges exist to report.
const controlPlaneSampleTimeout = 5 * time.Second

// ControlPlaneDBStats are the PostgreSQL numbers that predict a control-plane
// outage before it is visible as failed model loads.
type ControlPlaneDBStats struct {
	// OldestXminAge is transactions elapsed since the oldest snapshot any
	// backend still holds. While it grows, autovacuum can reclaim nothing in
	// the database, however often it runs. It was 21,002,291 during the
	// incident that motivated this.
	OldestXminAge int64
	// LongestTransactionSeconds is the age of the longest open transaction.
	LongestTransactionSeconds float64
	// DeadTupleRatio is dead tuples per live tuple, per control-plane table.
	DeadTupleRatio map[string]float64
}

// SampleControlPlaneDB reads the stats in two cheap catalog queries.
func SampleControlPlaneDB(ctx context.Context, db *gorm.DB) (ControlPlaneDBStats, error) {
	stats := ControlPlaneDBStats{DeadTupleRatio: map[string]float64{}}

	var horizon struct {
		XminAge  int64
		LongestS float64
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT
			COALESCE(MAX(age(backend_xmin)), 0)                              AS xmin_age,
			COALESCE(MAX(EXTRACT(EPOCH FROM now() - xact_start)), 0)         AS longest_s
		FROM pg_stat_activity
		WHERE datname = current_database()`).Scan(&horizon).Error; err != nil {
		return stats, err
	}
	stats.OldestXminAge = horizon.XminAge
	stats.LongestTransactionSeconds = horizon.LongestS

	var rows []struct {
		Relname string
		DeadTup int64
		LiveTup int64
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT relname, n_dead_tup AS dead_tup, n_live_tup AS live_tup
		FROM pg_stat_user_tables
		WHERE relname IN ?`, controlPlaneTables).Scan(&rows).Error; err != nil {
		return stats, err
	}
	for _, r := range rows {
		live := r.LiveTup
		if live < 1 {
			live = 1
		}
		stats.DeadTupleRatio[r.Relname] = float64(r.DeadTup) / float64(live)
	}
	return stats, nil
}

// cachedDBSampler serves the catalog sample to scrape-driven collection. The
// cache bounds how often a scrape can reach the database, and it keeps the
// last good values available when a later sample fails.
type cachedDBSampler struct {
	db          *gorm.DB
	minInterval time.Duration

	mu       sync.Mutex
	cached   ControlPlaneDBStats
	cachedAt time.Time
}

// stats returns the values to report and whether any good sample exists yet.
// A failed sample is not fatal: these gauges matter most when the database is
// struggling, which is exactly when this query can fail, so the last good
// values keep being reported instead of failing the whole scrape. Before the
// first good sample there is nothing honest to report, and reporting zero
// would read as a healthy horizon, so the caller reports nothing at all.
func (c *cachedDBSampler) stats(ctx context.Context) (ControlPlaneDBStats, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.cachedAt) >= c.minInterval {
		sampleCtx, cancel := context.WithTimeout(ctx, controlPlaneSampleTimeout)
		s, err := SampleControlPlaneDB(sampleCtx, c.db)
		cancel()
		if err != nil {
			xlog.Debug("Control-plane database sample failed, reporting the last good values", "error", err)
		} else {
			c.cached, c.cachedAt = s, time.Now()
		}
	}

	return c.cached, !c.cachedAt.IsZero()
}

// RegisterControlPlaneDBMetrics installs observable gauges backed by a cached
// sample. Collection is scrape-driven, so the cache bounds how often a scrape
// can reach the database.
func RegisterControlPlaneDBMetrics(db *gorm.DB, minInterval time.Duration) error {
	meter := otel.Meter("github.com/mudler/LocalAI")

	xminAge, err := meter.Int64ObservableGauge("localai_control_plane_oldest_xmin_age",
		metric.WithDescription("Transactions elapsed since the oldest snapshot still held. While this grows, autovacuum can reclaim nothing."))
	if err != nil {
		return err
	}
	longest, err := meter.Float64ObservableGauge("localai_control_plane_longest_transaction_seconds",
		metric.WithDescription("Age of the longest open transaction, in seconds."))
	if err != nil {
		return err
	}
	deadRatio, err := meter.Float64ObservableGauge("localai_control_plane_dead_tuple_ratio",
		metric.WithDescription("Dead tuples per live tuple on the control-plane registry tables."))
	if err != nil {
		return err
	}

	sampler := &cachedDBSampler{db: db, minInterval: minInterval}

	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		cached, ok := sampler.stats(ctx)
		if !ok {
			return nil
		}

		o.ObserveInt64(xminAge, cached.OldestXminAge)
		o.ObserveFloat64(longest, cached.LongestTransactionSeconds)
		for table, ratio := range cached.DeadTupleRatio {
			o.ObserveFloat64(deadRatio, ratio, metric.WithAttributes(attribute.String("table", table)))
		}
		return nil
	}, xminAge, longest, deadRatio)
	return err
}
