package failover

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/metric/noop"
)

var _ = Describe("metrics", func() {
	It("registers and records without a meter provider", func() {
		src := newFakeSource(remote("a"), local("b"), chainCfg("chain", nil, t("a"), t("b")))
		m := New(src, WithClock(newFakeClock()))
		Expect(func() { RegisterMetrics(m, noop.NewMeterProvider().Meter("test")) }).ToNot(Panic())
		Expect(func() { m.ReportFailure("a", errBoom) }).ToNot(Panic())
	})
	It("records an attempt trace only when enabled", func() {
		Expect(func() { RecordAttemptTrace(false, "chain", "a", errBoom) }).ToNot(Panic())
		Expect(func() { RecordAttemptTrace(true, "chain", "a", errBoom) }).ToNot(Panic())
	})
})
