package datamodel

import (
	"context"

	gopsutil "github.com/shirou/gopsutil/v3/process"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type BackendMonitorRequest struct {
	Model string `json:"model" yaml:"model"`
}

type BackendMonitorResponse struct {
	MemoryInfo    *gopsutil.MemoryInfoStat
	MemoryPercent float32
	CPUPercent    float64
}

type TTSRequest struct {
	Model   string `json:"model" yaml:"model"`
	Input   string `json:"input" yaml:"input"`
	Backend string `json:"backend" yaml:"backend"`
}

type LocalAIMetrics struct {
	Meter         metric.Meter
	ApiTimeMetric metric.Float64Histogram
}

func (m *LocalAIMetrics) ObserveAPICall(method string, path string, duration float64) {
	opts := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", path),
	)
	m.ApiTimeMetric.Record(context.Background(), duration, opts)
}
