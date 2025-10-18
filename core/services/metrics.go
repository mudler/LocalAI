package services

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricApi "go.opentelemetry.io/otel/sdk/metric"
)

// MetricsStore is the interface for storing and retrieving metrics
// This allows for future implementations with persistence (JSON files, databases, etc.)
type MetricsStore interface {
	RecordRequest(endpoint, model, backend string, success bool, duration time.Duration)
	GetEndpointStats() map[string]int64
	GetModelStats() map[string]int64
	GetBackendStats() map[string]int64
	GetRequestsOverTime(hours int) []TimeSeriesPoint
	GetTotalRequests() int64
	GetSuccessRate() float64
	Reset()
}

// TimeSeriesPoint represents a single point in the time series
type TimeSeriesPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int64     `json:"count"`
}

// RequestRecord stores individual request information
type RequestRecord struct {
	Timestamp time.Time
	Endpoint  string
	Model     string
	Backend   string
	Success   bool
	Duration  time.Duration
}

// InMemoryMetricsStore implements MetricsStore with in-memory storage
type InMemoryMetricsStore struct {
	endpoints    map[string]int64
	models       map[string]int64
	backends     map[string]int64
	timeSeries   []RequestRecord
	successCount int64
	failureCount int64
	mu           sync.RWMutex
	stopChan     chan struct{}
	maxRecords   int           // Maximum number of time series records to keep
	maxMapKeys   int           // Maximum number of unique keys per map
	pruneEvery   time.Duration // How often to prune old data
}

// NewInMemoryMetricsStore creates a new in-memory metrics store
func NewInMemoryMetricsStore() *InMemoryMetricsStore {
	store := &InMemoryMetricsStore{
		endpoints:  make(map[string]int64),
		models:     make(map[string]int64),
		backends:   make(map[string]int64),
		timeSeries: make([]RequestRecord, 0),
		stopChan:   make(chan struct{}),
		maxRecords: 10000,           // Limit to 10k records (~1-2MB of memory)
		maxMapKeys: 1000,            // Limit to 1000 unique keys per map (~50KB per map)
		pruneEvery: 5 * time.Minute, // Prune every 5 minutes instead of every request
	}

	// Start background pruning goroutine
	go store.pruneLoop()

	return store
}

// pruneLoop runs periodically to clean up old data
func (m *InMemoryMetricsStore) pruneLoop() {
	ticker := time.NewTicker(m.pruneEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.pruneOldData()
		case <-m.stopChan:
			return
		}
	}
}

// pruneOldData removes data older than 24 hours and enforces max record limit
func (m *InMemoryMetricsStore) pruneOldData() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-24 * time.Hour)
	newTimeSeries := make([]RequestRecord, 0, len(m.timeSeries))

	for _, r := range m.timeSeries {
		if r.Timestamp.After(cutoff) {
			newTimeSeries = append(newTimeSeries, r)
		}
	}

	// If still over the limit, keep only the most recent records
	if len(newTimeSeries) > m.maxRecords {
		// Keep the most recent maxRecords entries
		newTimeSeries = newTimeSeries[len(newTimeSeries)-m.maxRecords:]
		log.Warn().
			Int("dropped", len(m.timeSeries)-len(newTimeSeries)).
			Int("kept", len(newTimeSeries)).
			Msg("Metrics store exceeded maximum records, dropping oldest entries")
	}

	m.timeSeries = newTimeSeries

	// Also check if maps have grown too large
	m.pruneMapIfNeeded("endpoints", m.endpoints, m.maxMapKeys)
	m.pruneMapIfNeeded("models", m.models, m.maxMapKeys)
	m.pruneMapIfNeeded("backends", m.backends, m.maxMapKeys)
}

// pruneMapIfNeeded keeps only the top N entries in a map by count
func (m *InMemoryMetricsStore) pruneMapIfNeeded(name string, mapData map[string]int64, maxKeys int) {
	if len(mapData) <= maxKeys {
		return
	}

	// Convert to slice for sorting
	type kv struct {
		key   string
		value int64
	}

	entries := make([]kv, 0, len(mapData))
	for k, v := range mapData {
		entries = append(entries, kv{k, v})
	}

	// Sort by value descending (keep highest counts)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].value < entries[j].value {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Keep only top maxKeys entries
	for k := range mapData {
		delete(mapData, k)
	}

	for i := 0; i < maxKeys && i < len(entries); i++ {
		mapData[entries[i].key] = entries[i].value
	}

	log.Warn().
		Str("map", name).
		Int("dropped", len(entries)-maxKeys).
		Int("kept", maxKeys).
		Msg("Metrics map exceeded maximum keys, keeping only top entries")
}

// Stop gracefully shuts down the metrics store
func (m *InMemoryMetricsStore) Stop() {
	close(m.stopChan)
}

// RecordRequest records a new API request
func (m *InMemoryMetricsStore) RecordRequest(endpoint, model, backend string, success bool, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record endpoint
	if endpoint != "" {
		m.endpoints[endpoint]++
	}

	// Record model
	if model != "" {
		m.models[model]++
	}

	// Record backend
	if backend != "" {
		m.backends[backend]++
	}

	// Record success/failure
	if success {
		m.successCount++
	} else {
		m.failureCount++
	}

	// Add to time series
	record := RequestRecord{
		Timestamp: time.Now(),
		Endpoint:  endpoint,
		Model:     model,
		Backend:   backend,
		Success:   success,
		Duration:  duration,
	}
	m.timeSeries = append(m.timeSeries, record)

	// Note: Pruning is done periodically by pruneLoop() to avoid overhead on every request
}

// GetEndpointStats returns request counts per endpoint
func (m *InMemoryMetricsStore) GetEndpointStats() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]int64)
	for k, v := range m.endpoints {
		result[k] = v
	}
	return result
}

// GetModelStats returns request counts per model
func (m *InMemoryMetricsStore) GetModelStats() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]int64)
	for k, v := range m.models {
		result[k] = v
	}
	return result
}

// GetBackendStats returns request counts per backend
func (m *InMemoryMetricsStore) GetBackendStats() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]int64)
	for k, v := range m.backends {
		result[k] = v
	}
	return result
}

// GetRequestsOverTime returns time series data for the specified number of hours
func (m *InMemoryMetricsStore) GetRequestsOverTime(hours int) []TimeSeriesPoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	// Group by hour
	hourlyBuckets := make(map[int64]int64)
	for _, record := range m.timeSeries {
		if record.Timestamp.After(cutoff) {
			// Round down to the hour
			hourTimestamp := record.Timestamp.Truncate(time.Hour).Unix()
			hourlyBuckets[hourTimestamp]++
		}
	}

	// Convert to sorted time series
	result := make([]TimeSeriesPoint, 0)
	for ts, count := range hourlyBuckets {
		result = append(result, TimeSeriesPoint{
			Timestamp: time.Unix(ts, 0),
			Count:     count,
		})
	}

	// Sort by timestamp
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Timestamp.After(result[j].Timestamp) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// GetTotalRequests returns the total number of requests recorded
func (m *InMemoryMetricsStore) GetTotalRequests() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.successCount + m.failureCount
}

// GetSuccessRate returns the percentage of successful requests
func (m *InMemoryMetricsStore) GetSuccessRate() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := m.successCount + m.failureCount
	if total == 0 {
		return 0.0
	}
	return float64(m.successCount) / float64(total) * 100.0
}

// Reset clears all metrics
func (m *InMemoryMetricsStore) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.endpoints = make(map[string]int64)
	m.models = make(map[string]int64)
	m.backends = make(map[string]int64)
	m.timeSeries = make([]RequestRecord, 0)
	m.successCount = 0
	m.failureCount = 0
}

// ============================================================================
// OpenTelemetry Metrics Service (for Prometheus export)
// ============================================================================

type LocalAIMetricsService struct {
	Meter         metric.Meter
	ApiTimeMetric metric.Float64Histogram
}

func (m *LocalAIMetricsService) ObserveAPICall(method string, path string, duration float64) {
	opts := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", path),
	)
	m.ApiTimeMetric.Record(context.Background(), duration, opts)
}

// NewLocalAIMetricsService bootstraps the OpenTelemetry pipeline for Prometheus export.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func NewLocalAIMetricsService() (*LocalAIMetricsService, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}
	provider := metricApi.NewMeterProvider(metricApi.WithReader(exporter))
	meter := provider.Meter("github.com/mudler/LocalAI")

	apiTimeMetric, err := meter.Float64Histogram("api_call", metric.WithDescription("api calls"))
	if err != nil {
		return nil, err
	}

	return &LocalAIMetricsService{
		Meter:         meter,
		ApiTimeMetric: apiTimeMetric,
	}, nil
}

func (lams LocalAIMetricsService) Shutdown() error {
	// TODO: Not sure how to actually do this:
	//// setupOTelSDK bootstraps the OpenTelemetry pipeline.
	//// If it does not return an error, make sure to call shutdown for proper cleanup.

	log.Warn().Msgf("LocalAIMetricsService Shutdown called, but OTelSDK proper shutdown not yet implemented?")
	return nil
}
