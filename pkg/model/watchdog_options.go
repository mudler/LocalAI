package model

import (
	"time"
)

const DefaultWatchdogInterval = 500 * time.Millisecond

// WatchDogOptions contains all configuration for the WatchDog
type WatchDogOptions struct {
	processManager ProcessManager

	// Timeout settings
	busyTimeout      time.Duration
	idleTimeout      time.Duration
	watchdogInterval time.Duration

	// Check toggles
	busyCheck bool
	idleCheck bool

	// LRU settings
	lruLimit int // Maximum number of active backends (0 = unlimited)

	// Memory reclaimer settings (works with GPU if available, otherwise RAM)
	memoryReclaimerEnabled   bool    // Enable memory threshold monitoring
	memoryReclaimerThreshold float64 // Threshold 0.0-1.0 (e.g., 0.95 = 95%)
}

// WatchDogOption is a function that configures WatchDogOptions
type WatchDogOption func(*WatchDogOptions)

// WithProcessManager sets the process manager for the watchdog
func WithProcessManager(pm ProcessManager) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.processManager = pm
	}
}

// WithBusyTimeout sets the busy timeout duration
func WithBusyTimeout(timeout time.Duration) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.busyTimeout = timeout
	}
}

// WithIdleTimeout sets the idle timeout duration
func WithIdleTimeout(timeout time.Duration) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.idleTimeout = timeout
	}
}

// WithWatchdogCheck sets the watchdog check duration
func WithWatchdogInterval(interval time.Duration) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.watchdogInterval = interval
	}
}

// WithBusyCheck enables or disables busy checking
func WithBusyCheck(enabled bool) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.busyCheck = enabled
	}
}

// WithIdleCheck enables or disables idle checking
func WithIdleCheck(enabled bool) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.idleCheck = enabled
	}
}

// WithLRULimit sets the maximum number of active backends (0 = unlimited)
func WithLRULimit(limit int) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.lruLimit = limit
	}
}

// WithMemoryReclaimer enables memory threshold monitoring with the specified threshold
// Works with GPU VRAM if available, otherwise uses system RAM
func WithMemoryReclaimer(enabled bool, threshold float64) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.memoryReclaimerEnabled = enabled
		o.memoryReclaimerThreshold = threshold
	}
}

// WithMemoryReclaimerEnabled enables or disables memory threshold monitoring
func WithMemoryReclaimerEnabled(enabled bool) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.memoryReclaimerEnabled = enabled
	}
}

// WithMemoryReclaimerThreshold sets the memory threshold (0.0-1.0)
func WithMemoryReclaimerThreshold(threshold float64) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.memoryReclaimerThreshold = threshold
	}
}

// DefaultWatchDogOptions returns default options for the watchdog
func DefaultWatchDogOptions() *WatchDogOptions {
	return &WatchDogOptions{
		busyTimeout:              5 * time.Minute,
		idleTimeout:              15 * time.Minute,
		watchdogInterval:         DefaultWatchdogInterval,
		busyCheck:                false,
		idleCheck:                false,
		lruLimit:                 0,
		memoryReclaimerEnabled:   false,
		memoryReclaimerThreshold: 0.80,
	}
}

// NewWatchDogOptions creates WatchDogOptions with the provided options applied
func NewWatchDogOptions(opts ...WatchDogOption) *WatchDogOptions {
	o := DefaultWatchDogOptions()
	for _, opt := range opts {
		opt(o)
	}
	return o
}
