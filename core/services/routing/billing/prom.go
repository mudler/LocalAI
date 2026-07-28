package billing

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/contract"
	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Recorder is the single increment site for billing data. It writes
// the same record to (a) the StatsBackend (durable / queryable) and
// (b) Prometheus counters (live ops). Splitting these would invite
// drift; this type guarantees both fire in lockstep from one call.
//
// The plan calls out a DB-vs-Prom drift assertion. With a single
// increment site, drift can only come from StatsBackend.Record returning
// without persisting (e.g., the DB flusher dropping batches under load
// — see gormBackend.flush). We log+invariant-fail in that path; a future
// drift goroutine compares Prom to a SUM(total_tokens) checkpoint as
// extra defense in depth.
type Recorder struct {
	backend StatsBackend

	tokensCounter metric.Int64Counter
	costCounter   metric.Float64Counter
	requestsCount metric.Int64Counter
}

var (
	metricsOnce              sync.Once
	sharedTokensCounter      metric.Int64Counter
	sharedCostCounter        metric.Float64Counter
	sharedRequestsCount      metric.Int64Counter
	sharedUnrecordedCounter  metric.Int64Counter

	// configuredMeter is the meter handed in by the caller (typically
	// monitoring.LocalAIMetricsService). Setting it before initMetrics
	// runs makes sure billing's counters land on the same Prom-backed
	// MeterProvider that exports /metrics. Without this we relied on
	// otel.SetMeterProvider race ordering, which silently dropped
	// counters when initMetrics ran first.
	configuredMeterMu sync.Mutex
	configuredMeter   metric.Meter
)

// SetMeter wires the meter from monitoring.LocalAIMetricsService (or any
// caller-controlled MeterProvider) before any Recorder is constructed.
// Call from application startup — initMetrics uses this meter rather than
// the OTel global the moment it's set.
func SetMeter(m metric.Meter) {
	configuredMeterMu.Lock()
	defer configuredMeterMu.Unlock()
	configuredMeter = m
}

func resolveMeter() metric.Meter {
	configuredMeterMu.Lock()
	m := configuredMeter
	configuredMeterMu.Unlock()
	if m != nil {
		return m
	}
	return otel.Meter("github.com/mudler/LocalAI/core/services/routing/billing")
}

func initMetrics() {
	metricsOnce.Do(func() {
		meter := resolveMeter()
		var err error
		sharedTokensCounter, err = meter.Int64Counter(
			"localai_tokens_total",
			metric.WithDescription("Cumulative tokens accounted, labeled by user, served_model, kind"),
		)
		if err != nil {
			xlog.Error("billing: failed to create tokens counter", "error", err)
		}
		sharedCostCounter, err = meter.Float64Counter(
			"localai_cost_usd_total",
			metric.WithDescription("Cumulative USD cost accounted, labeled by user, served_model"),
		)
		if err != nil {
			xlog.Error("billing: failed to create cost counter", "error", err)
		}
		sharedRequestsCount, err = meter.Int64Counter(
			"localai_billed_requests_total",
			metric.WithDescription("Cumulative billed requests, labeled by user, served_model, endpoint"),
		)
		if err != nil {
			xlog.Error("billing: failed to create requests counter", "error", err)
		}
		sharedUnrecordedCounter, err = meter.Int64Counter(
			"localai_usage_unrecorded_total",
			metric.WithDescription("Requests that completed but produced no UsageRecord, labeled by endpoint and reason. A non-zero rate signals a billing gap (handler didn't stamp, body lacked usage, no user resolvable)."),
		)
		if err != nil {
			xlog.Error("billing: failed to create unrecorded counter", "error", err)
		}
	})
}

// CountUnrecorded ticks the localai_usage_unrecorded_total counter so that
// silent billing misses are observable. UsageMiddleware calls this whenever
// a request completes without producing a UsageRecord. Reasons should be
// short, stable strings ("no_handler_stamp", "no_user", "parse_failed", …)
// — never user-supplied content.
func CountUnrecorded(ctx context.Context, endpoint, reason string) {
	initMetrics()
	if sharedUnrecordedCounter == nil {
		return
	}
	sharedUnrecordedCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("endpoint", endpoint),
			attribute.String("reason", reason),
		))
}

// NewRecorder returns a Recorder that fans out to the given StatsBackend
// and to Prometheus. The Prom counters are package-singletons so that
// multiple Recorders (e.g., reusing the same metrics across rebuilds)
// don't double-register identical metric names.
func NewRecorder(backend StatsBackend) *Recorder {
	initMetrics()
	return &Recorder{
		backend:       backend,
		tokensCounter: sharedTokensCounter,
		costCounter:   sharedCostCounter,
		requestsCount: sharedRequestsCount,
	}
}

// Record asserts billing invariants, persists the record, and emits the
// matching Prom counters. r must not be mutated by the caller after
// this call; the backend takes ownership.
func (rec *Recorder) Record(ctx context.Context, r *auth.UsageRecord) error {
	rec.assertInvariants(r)

	if err := rec.backend.Record(ctx, r); err != nil {
		return err
	}

	if rec.tokensCounter != nil {
		userAttr := attribute.String("user", r.UserID)
		modelAttr := attribute.String("served_model", servedModelOf(r))
		rec.tokensCounter.Add(ctx, r.PromptTokens,
			metric.WithAttributes(userAttr, modelAttr, attribute.String("kind", "prompt")))
		rec.tokensCounter.Add(ctx, r.CompletionTokens,
			metric.WithAttributes(userAttr, modelAttr, attribute.String("kind", "completion")))
	}
	if rec.costCounter != nil && r.PricingVersionID != "" {
		rec.costCounter.Add(ctx, r.CostUSD,
			metric.WithAttributes(
				attribute.String("user", r.UserID),
				attribute.String("served_model", servedModelOf(r)),
			))
	}
	if rec.requestsCount != nil {
		rec.requestsCount.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("user", r.UserID),
				attribute.String("served_model", servedModelOf(r)),
				attribute.String("endpoint", r.Endpoint),
			))
	}
	return nil
}

// Aggregate is a convenience pass-through.
func (rec *Recorder) Aggregate(ctx context.Context, q AggregateQuery) ([]auth.UsageBucket, error) {
	return rec.backend.Aggregate(ctx, q)
}

// Close flushes the underlying backend.
func (rec *Recorder) Close() error { return rec.backend.Close() }

func (rec *Recorder) assertInvariants(r *auth.UsageRecord) {
	contract.Invariant(
		"billing.user_id_present",
		r.UserID != "",
		"endpoint", r.Endpoint, "model", r.Model,
	)
	// PII can only shrink the prompt; a post-filter count above pre-filter
	// would mean the filter expanded text, which is impossible by design.
	// Both are zero on legacy paths that don't populate the new fields,
	// so the assertion only fires when one side is set.
	if r.PreFilterPromptTokens > 0 || r.PostFilterPromptTokens > 0 {
		contract.Invariant(
			"billing.prefilter_ge_postfilter",
			r.PreFilterPromptTokens >= r.PostFilterPromptTokens,
			"pre", r.PreFilterPromptTokens, "post", r.PostFilterPromptTokens,
			"user", r.UserID, "model", r.Model,
		)
	}
	// CostUSD without a pricing version is a data-integrity bug: we'd
	// be unable to retroactively recompute or audit the rate used.
	if r.CostUSD != 0 {
		contract.Invariant(
			"billing.cost_requires_pricing_version",
			r.PricingVersionID != "",
			"cost", r.CostUSD, "model", r.Model,
		)
	}
}

func servedModelOf(r *auth.UsageRecord) string {
	if r.ServedModel != "" {
		return r.ServedModel
	}
	return r.Model
}
