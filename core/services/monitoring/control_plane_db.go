package monitoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"
)

// controlPlaneModels are the registry records whose table bloat degrades
// routing. The table NAMES are resolved from gorm rather than written out
// here, because the three do not agree on how they get one: BackendNode and
// NodeModel take gorm's default pluralisation, while GalleryOperationRecord
// overrides TableName. A literal list would keep compiling and silently stop
// matching the day any of them gains or changes a TableName, and a gauge that
// reads zero because it matched no rows looks exactly like a healthy cluster.
var controlPlaneModels = []any{
	&nodes.BackendNode{},
	&nodes.NodeModel{},
	&distributed.GalleryOperationRecord{},
}

// controlPlaneTableNames asks gorm what each model is actually stored as.
func controlPlaneTableNames(db *gorm.DB) ([]string, error) {
	names := make([]string, 0, len(controlPlaneModels))
	for _, model := range controlPlaneModels {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return nil, fmt.Errorf("resolving control-plane table name for %T: %w", model, err)
		}
		names = append(names, stmt.Table)
	}
	return names, nil
}

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
	tables, err := controlPlaneTableNames(db)
	if err != nil {
		return stats, err
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT relname, n_dead_tup AS dead_tup, n_live_tup AS live_tup
		FROM pg_stat_user_tables
		WHERE relname IN ?`, tables).Scan(&rows).Error; err != nil {
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
// cache bounds how often a scrape can reach the database, whether or not the
// sample succeeds, and it keeps the last good values available when a later
// sample fails.
type cachedDBSampler struct {
	db          *gorm.DB
	minInterval time.Duration

	mu     sync.Mutex
	cached ControlPlaneDBStats
	// hasSample records whether cached ever held a real reading. It is kept
	// apart from lastAttempt so that a failing database does not blank the
	// gauges, and so that a failed attempt still costs the interval.
	hasSample bool
	// lastAttempt times every attempt, successful or not. Gating on the last
	// success instead would leave a failing database open to one query per
	// scrape, which is a retry storm aimed at a database already in trouble.
	lastAttempt time.Time
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

	if c.lastAttempt.IsZero() || time.Since(c.lastAttempt) >= c.minInterval {
		sampleCtx, cancel := context.WithTimeout(ctx, controlPlaneSampleTimeout)
		s, err := SampleControlPlaneDB(sampleCtx, c.db)
		cancel()
		// Timed from completion, and recorded whatever the outcome, so that a
		// slow or failing sample is rate limited exactly like a good one.
		c.lastAttempt = time.Now()
		if err != nil {
			xlog.Debug("Control-plane database sample failed, reporting the last good values", "error", err)
		} else {
			c.cached, c.hasSample = s, true
		}
	}

	return c.cached, c.hasSample
}

// RegisterControlPlaneDBMetrics installs observable gauges backed by a cached
// sample. Collection is scrape-driven, so the cache bounds how often a scrape
// can reach the database.
func RegisterControlPlaneDBMetrics(db *gorm.DB, minInterval time.Duration) error {
	// ORDERING DEPENDENCY, deliberately recorded because nothing enforces it:
	// this reads the GLOBAL meter provider, so it must run after
	// monitoring.NewLocalAIMetricsService has called otel.SetMeterProvider in
	// core/application/startup.go. Called before that, the gauges bind to the
	// no-op global and never reach /metrics, silently. Today the distributed
	// wiring in core/application/distributed.go runs after that point, which is
	// why the global is safe here rather than injected the way billing, pii and
	// agentpool take an explicit meter. This repo has been bitten by that race
	// three times already: see the comments at core/application/startup.go,
	// core/http/app.go and core/application/application.go. Anyone moving this
	// call earlier must switch it to an injected meter instead.
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
