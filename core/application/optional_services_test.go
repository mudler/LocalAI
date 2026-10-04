// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

var _ = Describe("optional startup services", func() {
	DescribeTable("router log without billing stats", func(enabled bool) {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err := New(config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint, config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithRouterDecisionLog(enabled))
		if app != nil {
			DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(app.StatsRecorder()).To(BeNil())
		if enabled {
			Expect(app.RouterDecisions()).NotTo(BeNil())
		} else {
			Expect(app.RouterDecisions()).To(BeNil())
		}
	}, Entry("retained by explicit opt-in", true), Entry("disabled by default", false))
})

type registrationMeter struct {
	metric.Meter
	gauges int
}

func (m *registrationMeter) Int64ObservableGauge(name string, opts ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	m.gauges++
	return m.Meter.Int64ObservableGauge(name, opts...)
}

var _ = Describe("optional failover metrics", func() {
	It("registers only on the application's enabled meter", func() {
		meter := &registrationMeter{Meter: noop.NewMeterProvider().Meter("test")}
		app := &Application{applicationConfig: &config.ApplicationConfig{DisableMetrics: true}, metricsService: &monitoring.LocalAIMetricsService{Meter: meter}}
		app.registerFailoverMetrics()
		Expect(meter.gauges).To(BeZero())
		app.applicationConfig.DisableMetrics = false
		app.registerFailoverMetrics()
		Expect(meter.gauges).To(Equal(1))
	})
})
