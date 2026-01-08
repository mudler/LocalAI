package model

import (
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
)

// WatchDog tracks all the requests from GRPC clients.
// All GRPC Clients created by ModelLoader should have an associated injected
// watchdog that will keep track of the state of each backend (busy or not)
// and for how much time it has been busy.
// If a backend is busy for too long, the watchdog will kill the process and
// force a reload of the model.
// The watchdog also supports LRU (Least Recently Used) eviction when a maximum
// number of active backends is configured.
// The watchdog also supports memory threshold monitoring - when memory usage
// (GPU VRAM if available, otherwise system RAM) exceeds the threshold,
// it will evict backends using the LRU strategy.
// The watchdog runs as a separate go routine,
// and the GRPC client talks to it via a channel to send status updates
type WatchDog struct {
	sync.Mutex
	busyTime             map[string]time.Time
	idleTime             map[string]time.Time
	lastUsed             map[string]time.Time // LRU tracking: when each model was last used
	timeout, idletimeout time.Duration
	addressMap           map[string]*process.Process
	addressModelMap      map[string]string
	pm                   ProcessManager
	stop                 chan bool

	busyCheck, idleCheck bool
	lruLimit             int // Maximum number of active backends (0 = unlimited)

	// Memory reclaimer settings (works with GPU if available, otherwise RAM)
	memoryReclaimerEnabled   bool    // Enable memory threshold monitoring
	memoryReclaimerThreshold float64 // Threshold 0.0-1.0 (e.g., 0.95 = 95%)
	watchdogInterval         time.Duration

	// Eviction settings
	forceEvictionWhenBusy bool // Force eviction even when models have active API calls (default: false for safety)
}

type ProcessManager interface {
	ShutdownModel(modelName string) error
}

// NewWatchDog creates a new WatchDog with the provided options.
// Example usage:
//
//	wd := NewWatchDog(
//	    WithProcessManager(pm),
//	    WithBusyTimeout(5*time.Minute),
//	    WithIdleTimeout(15*time.Minute),
//	    WithBusyCheck(true),
//	    WithIdleCheck(true),
//	    WithLRULimit(3),
//	    WithMemoryReclaimer(true, 0.95),
//	)
func NewWatchDog(opts ...WatchDogOption) *WatchDog {
	o := NewWatchDogOptions(opts...)

	return &WatchDog{
		timeout:                  o.busyTimeout,
		idletimeout:              o.idleTimeout,
		pm:                       o.processManager,
		busyTime:                 make(map[string]time.Time),
		idleTime:                 make(map[string]time.Time),
		lastUsed:                 make(map[string]time.Time),
		addressMap:               make(map[string]*process.Process),
		busyCheck:                o.busyCheck,
		idleCheck:                o.idleCheck,
		lruLimit:                 o.lruLimit,
		addressModelMap:          make(map[string]string),
		stop:                     make(chan bool, 1),
		memoryReclaimerEnabled:   o.memoryReclaimerEnabled,
		memoryReclaimerThreshold: o.memoryReclaimerThreshold,
		watchdogInterval:         o.watchdogInterval,
		forceEvictionWhenBusy:    o.forceEvictionWhenBusy,
	}
}

// SetLRULimit updates the LRU limit dynamically
func (wd *WatchDog) SetLRULimit(limit int) {
	wd.Lock()
	defer wd.Unlock()
	wd.lruLimit = limit
}

// GetLRULimit returns the current LRU limit
func (wd *WatchDog) GetLRULimit() int {
	wd.Lock()
	defer wd.Unlock()
	return wd.lruLimit
}

// SetMemoryReclaimer updates the memory reclaimer settings dynamically
func (wd *WatchDog) SetMemoryReclaimer(enabled bool, threshold float64) {
	wd.Lock()
	defer wd.Unlock()
	wd.memoryReclaimerEnabled = enabled
	wd.memoryReclaimerThreshold = threshold
}

// GetMemoryReclaimerSettings returns the current memory reclaimer settings
func (wd *WatchDog) GetMemoryReclaimerSettings() (enabled bool, threshold float64) {
	wd.Lock()
	defer wd.Unlock()
	return wd.memoryReclaimerEnabled, wd.memoryReclaimerThreshold
}

// SetForceEvictionWhenBusy updates the force eviction when busy setting dynamically
func (wd *WatchDog) SetForceEvictionWhenBusy(force bool) {
	wd.Lock()
	defer wd.Unlock()
	wd.forceEvictionWhenBusy = force
}

func (wd *WatchDog) Shutdown() {
	wd.Lock()
	defer wd.Unlock()
	xlog.Info("[WatchDog] Shutting down watchdog")
	wd.stop <- true
}

func (wd *WatchDog) AddAddressModelMap(address string, model string) {
	wd.Lock()
	defer wd.Unlock()
	wd.addressModelMap[address] = model

}
func (wd *WatchDog) Add(address string, p *process.Process) {
	wd.Lock()
	defer wd.Unlock()
	wd.addressMap[address] = p
}

func (wd *WatchDog) Mark(address string) {
	wd.Lock()
	defer wd.Unlock()
	now := time.Now()
	wd.busyTime[address] = now
	wd.lastUsed[address] = now // Update LRU tracking
	delete(wd.idleTime, address)
}

func (wd *WatchDog) UnMark(ModelAddress string) {
	wd.Lock()
	defer wd.Unlock()
	now := time.Now()
	delete(wd.busyTime, ModelAddress)
	wd.idleTime[ModelAddress] = now
	wd.lastUsed[ModelAddress] = now // Update LRU tracking
}

// UpdateLastUsed updates the last used time for a model address (for LRU tracking)
// This should be called when a model is accessed (e.g., when checking if loaded)
func (wd *WatchDog) UpdateLastUsed(address string) {
	wd.Lock()
	defer wd.Unlock()
	wd.lastUsed[address] = time.Now()
}

// GetLoadedModelCount returns the number of currently loaded models tracked by the watchdog
func (wd *WatchDog) GetLoadedModelCount() int {
	wd.Lock()
	defer wd.Unlock()
	return len(wd.addressModelMap)
}

// modelUsageInfo holds information about a model's usage for LRU sorting
type modelUsageInfo struct {
	address  string
	model    string
	lastUsed time.Time
}

// EnforceLRULimitResult contains the result of LRU enforcement
type EnforceLRULimitResult struct {
	EvictedCount int  // Number of models successfully evicted
	NeedMore     bool // True if more evictions are needed but couldn't be done (e.g., all models are busy)
}

// EnforceLRULimit ensures we're under the LRU limit by evicting least recently used models.
// This should be called before loading a new model.
// pendingLoads is the number of models currently being loaded (to account for concurrent loads).
// Returns the result containing evicted count and whether more evictions are needed.
func (wd *WatchDog) EnforceLRULimit(pendingLoads int) EnforceLRULimitResult {
	if wd.lruLimit <= 0 {
		return EnforceLRULimitResult{EvictedCount: 0, NeedMore: false} // LRU disabled
	}

	wd.Lock()

	currentCount := len(wd.addressModelMap)
	// We need to evict enough to make room for the new model AND any pending loads
	// Total after loading = currentCount + pendingLoads + 1 (the new one we're about to load)
	// We need: currentCount + pendingLoads + 1 <= lruLimit
	// So evict: currentCount + pendingLoads + 1 - lruLimit = currentCount - lruLimit + pendingLoads + 1
	modelsToEvict := currentCount - wd.lruLimit + pendingLoads + 1
	forceEvictionWhenBusy := wd.forceEvictionWhenBusy
	if modelsToEvict <= 0 {
		wd.Unlock()
		return EnforceLRULimitResult{EvictedCount: 0, NeedMore: false}
	}

	xlog.Debug("[WatchDog] LRU enforcement triggered", "current", currentCount, "pendingLoads", pendingLoads, "limit", wd.lruLimit, "toEvict", modelsToEvict)

	// Build a list of models sorted by last used time (oldest first)
	var models []modelUsageInfo
	for address, model := range wd.addressModelMap {
		lastUsed := wd.lastUsed[address]
		if lastUsed.IsZero() {
			// If no lastUsed recorded, use a very old time
			lastUsed = time.Time{}
		}
		models = append(models, modelUsageInfo{
			address:  address,
			model:    model,
			lastUsed: lastUsed,
		})
	}

	// Sort by lastUsed time (oldest first)
	sort.Slice(models, func(i, j int) bool {
		return models[i].lastUsed.Before(models[j].lastUsed)
	})

	// Collect models to evict (the oldest ones)
	var modelsToShutdown []string
	evictedCount := 0
	skippedBusyCount := 0
	for i := 0; evictedCount < modelsToEvict && i < len(models); i++ {
		m := models[i]
		// Check if model is busy
		_, isBusy := wd.busyTime[m.address]
		if isBusy && !forceEvictionWhenBusy {
			// Skip eviction for busy models when forceEvictionWhenBusy is false
			xlog.Warn("[WatchDog] Skipping LRU eviction for busy model", "model", m.model, "reason", "model has active API calls")
			skippedBusyCount++
			continue
		}
		xlog.Info("[WatchDog] LRU evicting model", "model", m.model, "lastUsed", m.lastUsed, "busy", isBusy)
		modelsToShutdown = append(modelsToShutdown, m.model)
		// Clean up the maps while we have the lock
		wd.untrack(m.address)
		evictedCount++
	}
	needMore := evictedCount < modelsToEvict && skippedBusyCount > 0
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent deadlock
	for _, model := range modelsToShutdown {
		if err := wd.pm.ShutdownModel(model); err != nil {
			xlog.Error("[WatchDog] error shutting down model during LRU eviction", "error", err, "model", model)
		}
		xlog.Debug("[WatchDog] LRU eviction complete", "model", model)
	}

	if needMore {
		xlog.Warn("[WatchDog] LRU eviction incomplete", "evicted", evictedCount, "needed", modelsToEvict, "skippedBusy", skippedBusyCount, "reason", "some models are busy with active API calls")
	}

	return EnforceLRULimitResult{
		EvictedCount: len(modelsToShutdown),
		NeedMore:     needMore,
	}
}

func (wd *WatchDog) Run() {
	xlog.Info("[WatchDog] starting watchdog")

	for {
		select {
		case <-wd.stop:
			xlog.Info("[WatchDog] Stopping watchdog")
			return
		case <-time.After(wd.watchdogInterval):
			// Check if any monitoring is enabled
			wd.Lock()
			busyCheck := wd.busyCheck
			idleCheck := wd.idleCheck
			memoryCheck := wd.memoryReclaimerEnabled
			wd.Unlock()

			if !busyCheck && !idleCheck && !memoryCheck {
				xlog.Info("[WatchDog] No checks enabled, stopping watchdog")
				return
			}
			if busyCheck {
				wd.checkBusy()
			}
			if idleCheck {
				wd.checkIdle()
			}
			if memoryCheck {
				wd.checkMemory()
			}
		}
	}
}

func (wd *WatchDog) checkIdle() {
	wd.Lock()
	xlog.Debug("[WatchDog] Watchdog checks for idle connections")

	// Collect models to shutdown while holding the lock
	var modelsToShutdown []string
	for address, t := range wd.idleTime {
		xlog.Debug("[WatchDog] idle connection", "address", address)
		if time.Since(t) > wd.idletimeout {
			xlog.Warn("[WatchDog] Address is idle for too long, killing it", "address", address)
			model, ok := wd.addressModelMap[address]
			if ok {
				modelsToShutdown = append(modelsToShutdown, model)
			} else {
				xlog.Warn("[WatchDog] Address unresolvable", "address", address)
			}
			wd.untrack(address)
		}
	}
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent deadlock
	for _, model := range modelsToShutdown {
		if err := wd.pm.ShutdownModel(model); err != nil {
			xlog.Error("[watchdog] error shutting down model", "error", err, "model", model)
		}
		xlog.Debug("[WatchDog] model shut down", "model", model)
	}
}

func (wd *WatchDog) checkBusy() {
	wd.Lock()
	xlog.Debug("[WatchDog] Watchdog checks for busy connections")

	// Collect models to shutdown while holding the lock
	var modelsToShutdown []string
	for address, t := range wd.busyTime {
		xlog.Debug("[WatchDog] active connection", "address", address)

		if time.Since(t) > wd.timeout {
			model, ok := wd.addressModelMap[address]
			if ok {
				xlog.Warn("[WatchDog] Model is busy for too long, killing it", "model", model)
				modelsToShutdown = append(modelsToShutdown, model)
			} else {
				xlog.Warn("[WatchDog] Address unresolvable", "address", address)
			}
			wd.untrack(address)
		}
	}
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent deadlock
	for _, model := range modelsToShutdown {
		if err := wd.pm.ShutdownModel(model); err != nil {
			xlog.Error("[watchdog] error shutting down model", "error", err, "model", model)
		}
		xlog.Debug("[WatchDog] model shut down", "model", model)
	}
}

// checkMemory monitors memory usage (GPU VRAM if available, otherwise RAM) and evicts backends when usage exceeds threshold
func (wd *WatchDog) checkMemory() {
	wd.Lock()
	threshold := wd.memoryReclaimerThreshold
	enabled := wd.memoryReclaimerEnabled
	modelCount := len(wd.addressModelMap)
	wd.Unlock()

	if !enabled || threshold <= 0 || modelCount == 0 {
		return
	}

	// Get current memory usage (GPU if available, otherwise RAM)
	aggregate := xsysinfo.GetResourceAggregateInfo()
	if aggregate.TotalMemory == 0 {
		xlog.Debug("[WatchDog] No memory information available for memory reclaimer")
		return
	}

	// Convert threshold from 0.0-1.0 to percentage
	thresholdPercent := threshold * 100

	memoryType := "GPU"
	if aggregate.GPUCount == 0 {
		memoryType = "RAM"
	}

	xlog.Debug("[WatchDog] Memory check", "type", memoryType, "usage_percent", aggregate.UsagePercent, "threshold_percent", thresholdPercent, "loaded_models", modelCount)

	// Check if usage exceeds threshold
	if aggregate.UsagePercent > thresholdPercent {
		xlog.Warn("[WatchDog] Memory usage exceeds threshold, evicting LRU backend", "type", memoryType, "usage_percent", aggregate.UsagePercent, "threshold_percent", thresholdPercent)

		// Evict the least recently used model
		wd.evictLRUModel()
	}
}

// evictLRUModel evicts the least recently used model
func (wd *WatchDog) evictLRUModel() {
	wd.Lock()

	if len(wd.addressModelMap) == 0 {
		wd.Unlock()
		return
	}

	forceEvictionWhenBusy := wd.forceEvictionWhenBusy

	// Build a list of models sorted by last used time (oldest first)
	var models []modelUsageInfo
	for address, model := range wd.addressModelMap {
		lastUsed := wd.lastUsed[address]
		if lastUsed.IsZero() {
			lastUsed = time.Time{}
		}
		models = append(models, modelUsageInfo{
			address:  address,
			model:    model,
			lastUsed: lastUsed,
		})
	}

	if len(models) == 0 {
		wd.Unlock()
		return
	}

	// Sort by lastUsed time (oldest first)
	sort.Slice(models, func(i, j int) bool {
		return models[i].lastUsed.Before(models[j].lastUsed)
	})

	// Find the first non-busy model (or first model if forceEvictionWhenBusy is true)
	var lruModel *modelUsageInfo
	for i := 0; i < len(models); i++ {
		m := models[i]
		_, isBusy := wd.busyTime[m.address]
		if isBusy && !forceEvictionWhenBusy {
			// Skip busy models when forceEvictionWhenBusy is false
			xlog.Warn("[WatchDog] Skipping memory reclaimer eviction for busy model", "model", m.model, "reason", "model has active API calls")
			continue
		}
		lruModel = &m
		break
	}

	if lruModel == nil {
		// All models are busy and forceEvictionWhenBusy is false
		wd.Unlock()
		xlog.Warn("[WatchDog] Memory reclaimer cannot evict: all models are busy with active API calls")
		return
	}

	xlog.Info("[WatchDog] Memory reclaimer evicting LRU model", "model", lruModel.model, "lastUsed", lruModel.lastUsed)

	// Untrack the model
	wd.untrack(lruModel.address)
	wd.Unlock()

	// Shutdown the model
	if err := wd.pm.ShutdownModel(lruModel.model); err != nil {
		xlog.Error("[WatchDog] error shutting down model during memory reclamation", "error", err, "model", lruModel.model)
	} else {
		xlog.Info("[WatchDog] Memory reclaimer eviction complete", "model", lruModel.model)
	}
}

func (wd *WatchDog) untrack(address string) {
	delete(wd.busyTime, address)
	delete(wd.idleTime, address)
	delete(wd.lastUsed, address)
	delete(wd.addressModelMap, address)
	delete(wd.addressMap, address)
}
