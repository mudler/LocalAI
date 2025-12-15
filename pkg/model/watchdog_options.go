package model

import (
	"time"
)

// WatchDogOptions contains all configuration for the WatchDog
type WatchDogOptions struct {
	processManager ProcessManager

	// Timeout settings
	busyTimeout time.Duration
	idleTimeout time.Duration

	// Check toggles
	busyCheck bool
	idleCheck bool

	// LRU settings
	lruLimit int // Maximum number of active backends (0 = unlimited)

	// GPU reclaimer settings
	gpuReclaimerEnabled   bool    // Enable GPU memory threshold monitoring
	gpuReclaimerThreshold float64 // Threshold 0.0-1.0 (e.g., 0.95 = 95%)
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

// WithGPUReclaimer enables GPU memory threshold monitoring with the specified threshold
func WithGPUReclaimer(enabled bool, threshold float64) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.gpuReclaimerEnabled = enabled
		o.gpuReclaimerThreshold = threshold
	}
}

// WithGPUReclaimerEnabled enables or disables GPU memory threshold monitoring
func WithGPUReclaimerEnabled(enabled bool) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.gpuReclaimerEnabled = enabled
	}
}

// WithGPUReclaimerThreshold sets the GPU memory threshold (0.0-1.0)
func WithGPUReclaimerThreshold(threshold float64) WatchDogOption {
	return func(o *WatchDogOptions) {
		o.gpuReclaimerThreshold = threshold
	}
}

// DefaultWatchDogOptions returns default options for the watchdog
func DefaultWatchDogOptions() *WatchDogOptions {
	return &WatchDogOptions{
		busyTimeout:           5 * time.Minute,
		idleTimeout:           15 * time.Minute,
		busyCheck:             false,
		idleCheck:             false,
		lruLimit:              0,
		gpuReclaimerEnabled:   false,
		gpuReclaimerThreshold: 0.95,
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
