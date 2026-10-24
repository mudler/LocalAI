package model

import (
	"errors"
	"slices"
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
	busyTime       map[string]time.Time
	inFlight       map[string]int
	requestStarts  map[string]map[uint64]time.Time
	legacyRequests map[string][]uint64
	nextRequestID  uint64
	idleTime       map[string]time.Time
	lastUsed       map[string]time.Time // LRU tracking: when each model was last used

	// pendingEvictions counts, per address, the evictions detached from the
	// model registry whose stop has not returned yet. Idle and busy checks
	// skip these addresses so they neither re-evict nor wipe live request
	// state while the stop is in progress.
	pendingEvictions map[string]int

	timeout, idletimeout time.Duration
	addressMap           map[string]*process.Process
	addressModelMap      map[string]string
	pm                   ProcessManager
	stop                 chan bool
	done                 chan bool // Signals when Run() has completely shut down

	busyCheck, idleCheck bool
	lruLimit             int // Maximum number of active backends (0 = unlimited)

	// Memory reclaimer settings (works with GPU if available, otherwise RAM)
	memoryReclaimerEnabled   bool    // Enable memory threshold monitoring
	memoryReclaimerThreshold float64 // Threshold 0.0-1.0 (e.g., 0.95 = 95%)
	watchdogInterval         time.Duration

	// Eviction settings
	forceEvictionWhenBusy bool // Force eviction even when models have active API calls (default: false for safety)

	// Size-aware eviction: sort candidates by model file size (largest first) to maximize freed memory.
	// When enabled, bigger models are evicted before smaller ones regardless of recency.
	sizeAwareEviction bool
	modelSizes        map[string]int64 // modelID → file size in bytes

	// Pinned models are excluded from idle, LRU, and memory-pressure eviction
	pinnedModels map[string]bool

	// modelGroups maps a model name to its declared concurrency groups.
	// Two loaded models that share at least one group cannot coexist on this
	// node — see EnforceGroupExclusivity.
	modelGroups map[string][]string
}

type ProcessManager interface {
	ShutdownModel(modelName string) error
	// ShutdownModelForce stops the backend without waiting for an in-flight
	// gRPC call to finish. Used when the watchdog evicts a backend that is
	// stuck busy: the graceful ShutdownModel intentionally waits for active
	// calls until its bounded deadline.
	ShutdownModelForce(modelName string) error
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
		inFlight:                 make(map[string]int),
		requestStarts:            make(map[string]map[uint64]time.Time),
		legacyRequests:           make(map[string][]uint64),
		idleTime:                 make(map[string]time.Time),
		lastUsed:                 make(map[string]time.Time),
		pendingEvictions:         make(map[string]int),
		addressMap:               make(map[string]*process.Process),
		busyCheck:                o.busyCheck,
		idleCheck:                o.idleCheck,
		lruLimit:                 o.lruLimit,
		addressModelMap:          make(map[string]string),
		pinnedModels:             make(map[string]bool),
		modelGroups:              make(map[string][]string),
		stop:                     make(chan bool, 1),
		done:                     make(chan bool, 1),
		memoryReclaimerEnabled:   o.memoryReclaimerEnabled,
		memoryReclaimerThreshold: o.memoryReclaimerThreshold,
		watchdogInterval:         o.watchdogInterval,
		forceEvictionWhenBusy:    o.forceEvictionWhenBusy,
		sizeAwareEviction:        o.sizeAwareEviction,
		modelSizes:               make(map[string]int64),
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

// RegisterModelSize records the on-disk file size for a model.
// This is used by size-aware eviction to prefer evicting larger models first.
// Call this after a model has been successfully loaded.
func (wd *WatchDog) RegisterModelSize(modelID string, bytes int64) {
	wd.Lock()
	defer wd.Unlock()
	wd.modelSizes[modelID] = bytes
}

// SetSizeAwareEviction enables or disables size-aware eviction ordering.
// When enabled, eviction candidates are sorted by file size (largest first)
// rather than by recency, maximizing freed memory per eviction.
func (wd *WatchDog) SetSizeAwareEviction(enabled bool) {
	wd.Lock()
	defer wd.Unlock()
	wd.sizeAwareEviction = enabled
}

// GetSizeAwareEviction returns whether size-aware eviction is enabled.
func (wd *WatchDog) GetSizeAwareEviction() bool {
	wd.Lock()
	defer wd.Unlock()
	return wd.sizeAwareEviction
}

// SetPinnedModels replaces the set of pinned model names.
// Pinned models are excluded from idle, LRU, and memory-pressure eviction.
func (wd *WatchDog) SetPinnedModels(models []string) {
	wd.Lock()
	defer wd.Unlock()
	wd.pinnedModels = make(map[string]bool, len(models))
	for _, m := range models {
		wd.pinnedModels[m] = true
	}
}

// IsModelPinned returns true if the given model name is pinned
func (wd *WatchDog) IsModelPinned(modelName string) bool {
	wd.Lock()
	defer wd.Unlock()
	return wd.pinnedModels[modelName]
}

// ReplaceModelGroups replaces the per-model concurrency-group registry. The
// supplied map is copied; callers may mutate it after the call. Passing an
// empty or nil map clears all entries.
func (wd *WatchDog) ReplaceModelGroups(groups map[string][]string) {
	wd.Lock()
	defer wd.Unlock()
	wd.modelGroups = make(map[string][]string, len(groups))
	for name, gs := range groups {
		if len(gs) == 0 {
			continue
		}
		wd.modelGroups[name] = slices.Clone(gs)
	}
}

// GetModelGroups returns a copy of the concurrency groups configured for
// the given model, or nil if the model has no groups. The result may be
// freely mutated by the caller.
func (wd *WatchDog) GetModelGroups(modelName string) []string {
	wd.Lock()
	defer wd.Unlock()
	gs, ok := wd.modelGroups[modelName]
	if !ok || len(gs) == 0 {
		return nil
	}
	return slices.Clone(gs)
}

func (wd *WatchDog) Shutdown() {
	wd.Lock()
	defer wd.Unlock()
	xlog.Info("[WatchDog] Shutting down watchdog")
	wd.stop <- true
}

// WaitDone blocks until the watchdog's Run() goroutine has completely shut down.
// This should be called after Shutdown() to ensure the watchdog is fully stopped.
func (wd *WatchDog) WaitDone() {
	<-wd.done
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

// TrackRequest records one request and returns an idempotent completion
// callback. The per-request identity lets the busy watchdog follow the oldest
// request that is still active instead of treating uninterrupted parallel
// traffic as one indefinitely old request.
func (wd *WatchDog) TrackRequest(address string) func() {
	wd.Lock()
	requestID := wd.beginRequestLocked(address, false)
	wd.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			wd.Lock()
			defer wd.Unlock()
			wd.finishRequestLocked(address, requestID)
		})
	}
}

// Mark and UnMark retain the legacy public API for non-gRPC callers. Callers
// that can keep the completion callback should use TrackRequest so overlapping
// requests can complete in any order without losing their individual ages.
func (wd *WatchDog) Mark(address string) {
	wd.Lock()
	defer wd.Unlock()
	wd.beginRequestLocked(address, true)
}

func (wd *WatchDog) UnMark(address string) {
	wd.Lock()
	defer wd.Unlock()
	requests := wd.legacyRequests[address]
	if len(requests) == 0 {
		return
	}
	requestID := requests[len(requests)-1]
	requests = requests[:len(requests)-1]
	if len(requests) == 0 {
		delete(wd.legacyRequests, address)
	} else {
		wd.legacyRequests[address] = requests
	}
	wd.finishRequestLocked(address, requestID)
}

func (wd *WatchDog) beginRequestLocked(address string, legacy bool) uint64 {
	now := time.Now()
	wd.nextRequestID++
	requestID := wd.nextRequestID
	starts := wd.requestStarts[address]
	if starts == nil {
		starts = make(map[uint64]time.Time)
		wd.requestStarts[address] = starts
	}
	starts[requestID] = now
	if legacy {
		wd.legacyRequests[address] = append(wd.legacyRequests[address], requestID)
	}
	wd.inFlight[address] = len(starts)
	if len(starts) == 1 {
		wd.busyTime[address] = now
	}
	wd.lastUsed[address] = now
	delete(wd.idleTime, address)
	return requestID
}

func (wd *WatchDog) finishRequestLocked(address string, requestID uint64) {
	starts := wd.requestStarts[address]
	if _, exists := starts[requestID]; !exists {
		return
	}
	delete(starts, requestID)
	now := time.Now()
	wd.lastUsed[address] = now // Update LRU tracking
	if len(starts) > 0 {
		wd.inFlight[address] = len(starts)
		var oldest time.Time
		for _, started := range starts {
			if oldest.IsZero() || started.Before(oldest) {
				oldest = started
			}
		}
		wd.busyTime[address] = oldest
		return
	}
	delete(wd.requestStarts, address)
	delete(wd.inFlight, address)
	delete(wd.busyTime, address)
	wd.idleTime[address] = now
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

// WatchDogState holds the current state of models tracked by the watchdog
type WatchDogState struct {
	AddressModelMap map[string]string
	BusyTime        map[string]time.Time
	InFlight        map[string]int
	IdleTime        map[string]time.Time
	LastUsed        map[string]time.Time
	AddressMap      map[string]*process.Process
}

// GetState returns the current state of models tracked by the watchdog
// This can be used to restore state when creating a new watchdog
func (wd *WatchDog) GetState() WatchDogState {
	wd.Lock()
	defer wd.Unlock()

	// Create copies to avoid race conditions
	addressModelMap := make(map[string]string, len(wd.addressModelMap))
	for k, v := range wd.addressModelMap {
		addressModelMap[k] = v
	}

	busyTime := make(map[string]time.Time, len(wd.busyTime))
	for k, v := range wd.busyTime {
		busyTime[k] = v
	}

	inFlight := make(map[string]int, len(wd.inFlight))
	for k, v := range wd.inFlight {
		inFlight[k] = v
	}

	idleTime := make(map[string]time.Time, len(wd.idleTime))
	for k, v := range wd.idleTime {
		idleTime[k] = v
	}

	lastUsed := make(map[string]time.Time, len(wd.lastUsed))
	for k, v := range wd.lastUsed {
		lastUsed[k] = v
	}

	addressMap := make(map[string]*process.Process, len(wd.addressMap))
	for k, v := range wd.addressMap {
		addressMap[k] = v
	}

	return WatchDogState{
		AddressModelMap: addressModelMap,
		BusyTime:        busyTime,
		InFlight:        inFlight,
		IdleTime:        idleTime,
		LastUsed:        lastUsed,
		AddressMap:      addressMap,
	}
}

// RestoreState restores the model state from a previous watchdog
// This should be called after the new watchdog is created but before Run() is started
func (wd *WatchDog) RestoreState(state WatchDogState) {
	wd.Lock()
	defer wd.Unlock()

	wd.addressModelMap = state.AddressModelMap
	wd.busyTime = state.BusyTime
	wd.inFlight = state.InFlight
	if wd.inFlight == nil {
		wd.inFlight = make(map[string]int, len(wd.busyTime))
		for address := range wd.busyTime {
			wd.inFlight[address] = 1
		}
	}
	wd.requestStarts = make(map[string]map[uint64]time.Time, len(wd.inFlight))
	wd.legacyRequests = make(map[string][]uint64, len(wd.inFlight))
	for address, count := range wd.inFlight {
		started := wd.busyTime[address]
		if started.IsZero() {
			started = time.Now()
			wd.busyTime[address] = started
		}
		requests := make(map[uint64]time.Time, count)
		for range count {
			wd.nextRequestID++
			requestID := wd.nextRequestID
			requests[requestID] = started
			wd.legacyRequests[address] = append(wd.legacyRequests[address], requestID)
		}
		wd.requestStarts[address] = requests
	}
	wd.idleTime = state.IdleTime
	wd.lastUsed = state.LastUsed
	wd.addressMap = state.AddressMap

	xlog.Info("[WatchDog] Restored model state", "modelCount", len(wd.addressModelMap))
}

// modelUsageInfo holds information about a model's usage for eviction sorting
type modelUsageInfo struct {
	address   string
	model     string
	lastUsed  time.Time
	sizeBytes int64 // on-disk file size; 0 if unknown
}

// evictionTarget is a model selected for eviction, retaining whether it was busy
// at selection time. A busy target must be shut down via ShutdownModelForce:
// the graceful path waits for its in-flight gRPC call to finish, but a busy
// eviction only happens when that call is stuck (busy-killer) or the operator
// opted into forceEvictionWhenBusy — either way, waiting for the graceful
// deadline would defeat prompt eviction.
type evictionTarget struct {
	address string
	model   string
	wasBusy bool

	// detached is what the eviction removed from the model registry for this
	// address, kept so a shutdown that fails can register it again. nil only
	// when the address was not tracked.
	detached *detachedBackend
}

// detachedBackend is the identity of a backend whose eviction is pending. An
// eviction removes the backend from the model registry before it is stopped,
// so that concurrent LRU/group checks do not select it twice. A stop that fails
// (typically a lifecycle lock held by a long model load) leaves a backend that
// still runs and still holds its VRAM, so it must be registered again, or the
// next load sees no conflict and runs out of memory.
//
// Only the identity is detached. Request lifecycle state (in-flight requests,
// busy/idle/last-used times) stays live during the stop, so a request that
// completes or starts meanwhile updates it normally. Replaying a snapshot of
// that state after a failed stop would resurrect completed requests or erase
// requests started during the stop.
type detachedBackend struct {
	model   string
	process *process.Process
	size    int64
	hasSize bool
}

// detachForEvictionLocked removes the backend at address from the model
// registry and marks its eviction as pending. Must be called with wd.Lock()
// held.
func (wd *WatchDog) detachForEvictionLocked(address string) *detachedBackend {
	model, ok := wd.addressModelMap[address]
	if !ok {
		return nil
	}
	d := &detachedBackend{model: model, process: wd.addressMap[address]}
	d.size, d.hasSize = wd.modelSizes[model]
	wd.untrackModelLocked(address)
	wd.pendingEvictions[address]++
	return d
}

// finishEviction settles a pending eviction. keep registers the backend again
// because it is still running; otherwise the request state left for the
// address is cleared. Nothing is touched when a replacement backend registered
// the address in the meantime: it owns the address and its request state.
func (wd *WatchDog) finishEviction(t evictionTarget, keep bool) {
	wd.Lock()
	defer wd.Unlock()
	if wd.pendingEvictions[t.address] <= 1 {
		delete(wd.pendingEvictions, t.address)
	} else {
		wd.pendingEvictions[t.address]--
	}
	if _, taken := wd.addressModelMap[t.address]; taken {
		return
	}
	if !keep || t.detached == nil {
		wd.clearRequestStateLocked(t.address)
		return
	}
	d := t.detached
	wd.addressModelMap[t.address] = d.model
	if d.process != nil {
		wd.addressMap[t.address] = d.process
	}
	if d.hasSize {
		// The estimate was dropped only when no other address served this
		// model; never overwrite one a replacement has registered since.
		if _, ok := wd.modelSizes[d.model]; !ok {
			wd.modelSizes[d.model] = d.size
		}
	}
	xlog.Warn("[WatchDog] model could not be stopped, tracking it again", "model", d.model, "address", t.address)
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

	sizeAwareEviction := wd.sizeAwareEviction
	xlog.Debug("[WatchDog] LRU enforcement triggered", "current", currentCount, "pendingLoads", pendingLoads, "limit", wd.lruLimit, "toEvict", modelsToEvict, "sizeAware", sizeAwareEviction)

	// Build a list of models to sort for eviction candidates
	var models []modelUsageInfo
	for address, model := range wd.addressModelMap {
		lastUsed := wd.lastUsed[address]
		if lastUsed.IsZero() {
			lastUsed = time.Time{}
		}
		models = append(models, modelUsageInfo{
			address:   address,
			model:     model,
			lastUsed:  lastUsed,
			sizeBytes: wd.modelSizes[model],
		})
	}

	// Sort eviction candidates: largest-first when size-aware, oldest-first otherwise.
	// Tiebreaker in size-aware mode: oldest last-used (LRU) to break ties between
	// models of the same size.
	if sizeAwareEviction {
		slices.SortFunc(models, func(a, b modelUsageInfo) int {
			if a.sizeBytes != b.sizeBytes {
				return int(b.sizeBytes - a.sizeBytes) // largest first
			}
			return a.lastUsed.Compare(b.lastUsed) // oldest first as tiebreaker
		})
	} else {
		slices.SortFunc(models, func(a, b modelUsageInfo) int {
			return a.lastUsed.Compare(b.lastUsed)
		})
	}

	// Collect models to evict (the oldest ones)
	modelsToShutdown, skippedBusyCount := wd.collectEvictionsLocked(models, modelsToEvict, forceEvictionWhenBusy)
	needMore := len(modelsToShutdown) < modelsToEvict && skippedBusyCount > 0
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent
	// deadlock. Busy targets go through the force path so the loader doesn't
	// wait on their stuck in-flight call (which would re-deadlock here).
	stopped := wd.shutdownEvictedStopped(modelsToShutdown, "LRU eviction")
	if stopped < len(modelsToShutdown) {
		// A backend that would not stop is still running and still holds its
		// memory, so the room asked for does not exist yet.
		needMore = true
	}

	if needMore {
		xlog.Warn("[WatchDog] LRU eviction incomplete", "evicted", stopped, "needed", modelsToEvict, "skippedBusy", skippedBusyCount, "reason", "some models are busy with active API calls or could not be stopped")
	}

	return EnforceLRULimitResult{
		EvictedCount: stopped,
		NeedMore:     needMore,
	}
}

// collectEvictionsLocked walks `candidates` (already in eviction order) and
// untracks up to `maxToEvict` models that are eligible for eviction. Pinned
// models are always skipped; busy models are skipped unless `force` is true.
// Returns the evicted models (with their busy state at selection time) and
// the number skipped because they were busy. Must be called with wd.Lock()
// held.
func (wd *WatchDog) collectEvictionsLocked(candidates []modelUsageInfo, maxToEvict int, force bool) (evicted []evictionTarget, skippedBusy int) {
	for i := 0; len(evicted) < maxToEvict && i < len(candidates); i++ {
		m := candidates[i]
		if wd.pinnedModels[m.model] {
			xlog.Debug("[WatchDog] Skipping eviction for pinned model", "model", m.model)
			continue
		}
		_, isBusy := wd.busyTime[m.address]
		if isBusy && !force {
			xlog.Warn("[WatchDog] Skipping eviction for busy model", "model", m.model, "reason", "model has active API calls")
			skippedBusy++
			continue
		}
		xlog.Info("[WatchDog] evicting model", "model", m.model, "busy", isBusy)
		evicted = append(evicted, evictionTarget{address: m.address, model: m.model, wasBusy: isBusy, detached: wd.detachForEvictionLocked(m.address)})
	}
	return evicted, skippedBusy
}

// shutdownEvicted shuts down each evicted model, using the force path for any
// that were busy at selection time so a stuck in-flight call does not consume
// the graceful shutdown deadline.
func (wd *WatchDog) shutdownEvicted(targets []evictionTarget, label string) {
	wd.shutdownEvictedStopped(targets, label)
}

// shutdownEvictedStopped is shutdownEvicted that also reports how many models
// were really stopped. A model that failed to stop is still running, so it is
// tracked again and not counted: callers use the count to decide whether the
// room they asked for exists, and counting a backend that still holds its VRAM
// would let the next load start next to it.
func (wd *WatchDog) shutdownEvictedStopped(targets []evictionTarget, label string) (stopped int) {
	for _, t := range targets {
		err := wd.shutdownTarget(t)
		if err != nil && !errors.Is(err, ErrModelNotFound) {
			xlog.Error("[WatchDog] error shutting down model during "+label, "error", err, "model", t.model, "busy", t.wasBusy)
			if wd.stillResident(t) {
				wd.finishEviction(t, true)
			} else {
				// The loader already dropped the model before the error was
				// returned (a failed remote unload, or the remote half of a
				// local+remote placement). Tracking it again would leave a
				// phantom entry that inflates LRU counts and group conflicts.
				xlog.Warn("[WatchDog] model was already removed by the loader despite the shutdown error, not tracking it again", "model", t.model, "address", t.address)
				wd.finishEviction(t, false)
			}
			continue
		}
		wd.finishEviction(t, false)
		stopped++
		xlog.Debug("[WatchDog] "+label+" complete", "model", t.model, "busy", t.wasBusy)
	}
	return stopped
}

// stillResident reports whether the backend an eviction target describes is
// still held by the loader. A process manager that cannot answer is assumed to
// still hold it: after a failed stop, tracking a backend that may still run is
// the safe default, since forgetting it is what lets the next load run out of
// memory.
func (wd *WatchDog) stillResident(t evictionTarget) bool {
	if pm, ok := wd.pm.(interface {
		HasModelAtAddress(string, string) bool
	}); ok {
		return pm.HasModelAtAddress(t.model, t.address)
	}
	return true
}

// shutdownTarget retains the address selected under the watchdog lock. The
// loader checks it under its per-model lifecycle lock so a queued eviction
// cannot stop a backend loaded after the original target exited.
func (wd *WatchDog) shutdownTarget(t evictionTarget) error {
	if pm, ok := wd.pm.(interface {
		ShutdownModelAtAddress(string, string, bool) error
	}); ok {
		return pm.ShutdownModelAtAddress(t.model, t.address, t.wasBusy)
	}
	if t.wasBusy {
		return wd.pm.ShutdownModelForce(t.model)
	}
	return wd.pm.ShutdownModel(t.model)
}

// EnforceGroupExclusivity evicts every loaded model that shares at least one
// concurrency group with the requested model. The pinned/busy/retry semantics
// match EnforceLRULimit so the loader's retry loop can stay generic.
func (wd *WatchDog) EnforceGroupExclusivity(requestedModel string) EnforceLRULimitResult {
	wd.Lock()

	requestedGroups := wd.modelGroups[requestedModel]
	if len(requestedGroups) == 0 {
		wd.Unlock()
		return EnforceLRULimitResult{}
	}

	forceEvictionWhenBusy := wd.forceEvictionWhenBusy

	// Build the conflict candidate list: every loaded model whose groups
	// overlap with requestedGroups. Order doesn't affect correctness, but
	// sort by lastUsed (oldest first) so logs and behaviour are deterministic.
	var conflicts []modelUsageInfo
	for address, name := range wd.addressModelMap {
		if name == requestedModel {
			continue
		}
		if !groupsOverlap(requestedGroups, wd.modelGroups[name]) {
			continue
		}
		conflicts = append(conflicts, modelUsageInfo{
			address:  address,
			model:    name,
			lastUsed: wd.lastUsed[address],
		})
	}
	if len(conflicts) == 0 {
		wd.Unlock()
		return EnforceLRULimitResult{}
	}
	slices.SortFunc(conflicts, func(a, b modelUsageInfo) int {
		return a.lastUsed.Compare(b.lastUsed)
	})

	xlog.Debug("[WatchDog] Group exclusivity triggered", "requested", requestedModel, "groups", requestedGroups, "conflicts", len(conflicts))

	modelsToShutdown, skippedBusyCount := wd.collectEvictionsLocked(conflicts, len(conflicts), forceEvictionWhenBusy)
	// For groups any unresolved conflict matters — busy *or* pinned. The loader
	// retries on NeedMore; pinned cases will eventually time out and the load
	// proceeds with a visible warning, which is the right signal for what is a
	// configuration mismatch.
	needMore := len(modelsToShutdown) < len(conflicts)
	wd.Unlock()

	stopped := wd.shutdownEvictedStopped(modelsToShutdown, "group eviction")
	if stopped < len(modelsToShutdown) {
		// A conflict that would not stop is still running and still holds its
		// memory: report it as unresolved so the loader keeps waiting.
		needMore = true
	}

	if needMore {
		xlog.Warn("[WatchDog] Group eviction incomplete", "requested", requestedModel, "evicted", stopped, "needed", len(conflicts), "skippedBusy", skippedBusyCount, "reason", "some conflicts are busy, pinned or could not be stopped")
	}

	return EnforceLRULimitResult{
		EvictedCount: stopped,
		NeedMore:     needMore,
	}
}

// groupsOverlap reports whether the two group lists share any name.
func groupsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func (wd *WatchDog) Run() {
	xlog.Info("[WatchDog] starting watchdog")

	for {
		select {
		case <-wd.stop:
			xlog.Info("[WatchDog] Stopping watchdog")
			wd.done <- true
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
				wd.done <- true
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
	var modelsToShutdown []evictionTarget
	for address, t := range wd.idleTime {
		xlog.Debug("[WatchDog] idle connection", "address", address)
		if wd.pendingEvictions[address] > 0 {
			// Its stop is in progress; the eviction settles its state.
			continue
		}
		if time.Since(t) > wd.idletimeout {
			model, ok := wd.addressModelMap[address]
			if ok {
				if wd.pinnedModels[model] {
					xlog.Debug("[WatchDog] Skipping idle eviction for pinned model", "model", model)
					continue
				}
				xlog.Warn("[WatchDog] Address is idle for too long, killing it", "address", address, "model", model)
				modelsToShutdown = append(modelsToShutdown, evictionTarget{model: model, address: address, detached: wd.detachForEvictionLocked(address)})
			} else {
				xlog.Warn("[WatchDog] Address unresolvable", "address", address)
				wd.untrack(address)
			}
		}
	}
	wd.Unlock()

	wd.shutdownEvicted(modelsToShutdown, "idle timeout")
}

func (wd *WatchDog) checkBusy() {
	wd.Lock()
	xlog.Debug("[WatchDog] Watchdog checks for busy connections")

	// Collect models to shutdown while holding the lock
	var modelsToShutdown []evictionTarget
	for address, t := range wd.busyTime {
		xlog.Debug("[WatchDog] active connection", "address", address)
		if wd.pendingEvictions[address] > 0 {
			// Its stop is in progress; the eviction settles its state.
			continue
		}

		if time.Since(t) > wd.timeout {
			model, ok := wd.addressModelMap[address]
			if ok {
				xlog.Warn("[WatchDog] Model is busy for too long, killing it", "model", model)
				modelsToShutdown = append(modelsToShutdown, evictionTarget{model: model, address: address, wasBusy: true, detached: wd.detachForEvictionLocked(address)})
			} else {
				xlog.Warn("[WatchDog] Address unresolvable", "address", address)
				wd.untrack(address)
			}
		}
	}
	wd.Unlock()

	wd.shutdownEvicted(modelsToShutdown, "busy timeout")
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

	//xlog.Debug("[WatchDog] Memory check", "type", memoryType, "usage_percent", aggregate.UsagePercent, "threshold_percent", thresholdPercent, "loaded_models", modelCount)

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
	sizeAwareEviction := wd.sizeAwareEviction

	// Build a list of models to sort for eviction candidates
	var models []modelUsageInfo
	for address, model := range wd.addressModelMap {
		lastUsed := wd.lastUsed[address]
		if lastUsed.IsZero() {
			lastUsed = time.Time{}
		}
		models = append(models, modelUsageInfo{
			address:   address,
			model:     model,
			lastUsed:  lastUsed,
			sizeBytes: wd.modelSizes[model],
		})
	}

	if len(models) == 0 {
		wd.Unlock()
		return
	}

	// Sort eviction candidates: largest-first when size-aware, oldest-first otherwise.
	if sizeAwareEviction {
		slices.SortFunc(models, func(a, b modelUsageInfo) int {
			if a.sizeBytes != b.sizeBytes {
				return int(b.sizeBytes - a.sizeBytes) // largest first
			}
			return a.lastUsed.Compare(b.lastUsed)
		})
	} else {
		slices.SortFunc(models, func(a, b modelUsageInfo) int {
			return a.lastUsed.Compare(b.lastUsed)
		})
	}

	// Find the first non-busy, non-pinned model (or first non-pinned model if forceEvictionWhenBusy is true)
	var lruModel *modelUsageInfo
	for i := range len(models) {
		m := models[i]
		if wd.pinnedModels[m.model] {
			xlog.Debug("[WatchDog] Skipping memory reclaimer eviction for pinned model", "model", m.model)
			continue
		}
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

	// A busy target only gets here when forceEvictionWhenBusy is true, i.e.
	// the operator accepted evicting models with active calls. Route it
	// through the force path so the loader stops the process first instead
	// of blocking on the stuck in-flight call while holding its mutex.
	_, wasBusy := wd.busyTime[lruModel.address]

	wd.Unlock()

	// Shutdown the model. This path never untracks before the stop, so a
	// failed stop of a backend that still runs keeps it tracked on its own.
	target := evictionTarget{model: lruModel.model, address: lruModel.address, wasBusy: wasBusy}
	if err := wd.shutdownTarget(target); err != nil && !errors.Is(err, ErrModelNotFound) {
		xlog.Error("[WatchDog] error shutting down model during memory reclamation", "error", err, "model", lruModel.model)
		if !wd.stillResident(target) {
			// Same rule as shutdownEvictedStopped: the loader dropped the model
			// before returning the error, so keeping the entry would leave a
			// phantom that is counted as loaded.
			wd.Lock()
			wd.untrack(lruModel.address)
			wd.Unlock()
		}
	} else {
		// Untrack the model
		wd.Lock()
		wd.untrack(lruModel.address)
		wd.Unlock()
		xlog.Info("[WatchDog] Memory reclaimer eviction complete", "model", lruModel.model)
	}
}

func (wd *WatchDog) untrack(address string) {
	wd.untrackModelLocked(address)
	wd.clearRequestStateLocked(address)
}

// untrackModelLocked removes the backend identity registered at address.
func (wd *WatchDog) untrackModelLocked(address string) {
	if modelID, ok := wd.addressModelMap[address]; ok {
		// A stale address can coexist with the replacement's registration.
		// Its cleanup must not erase the replacement's size estimate.
		otherAddress := false
		for addr, name := range wd.addressModelMap {
			if addr != address && name == modelID {
				otherAddress = true
				break
			}
		}
		if !otherAddress {
			delete(wd.modelSizes, modelID)
		}
	}
	delete(wd.addressModelMap, address)
	delete(wd.addressMap, address)
}

// clearRequestStateLocked removes the request lifecycle state of address.
func (wd *WatchDog) clearRequestStateLocked(address string) {
	delete(wd.busyTime, address)
	delete(wd.inFlight, address)
	delete(wd.requestStarts, address)
	delete(wd.legacyRequests, address)
	delete(wd.idleTime, address)
	delete(wd.lastUsed, address)
}

// Untrack removes request and eviction state after a backend is removed.
func (wd *WatchDog) Untrack(address string) {
	wd.Lock()
	defer wd.Unlock()
	wd.untrack(address)
}

func (wd *WatchDog) untrackProcess(p *process.Process) {
	wd.Lock()
	defer wd.Unlock()
	for address, tracked := range wd.addressMap {
		if tracked == p {
			wd.untrack(address)
		}
	}
}
