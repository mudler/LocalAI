package model

import (
	"sort"
	"sync"
	"time"

	process "github.com/mudler/go-processmanager"
	"github.com/rs/zerolog/log"
)

// WatchDog tracks all the requests from GRPC clients.
// All GRPC Clients created by ModelLoader should have an associated injected
// watchdog that will keep track of the state of each backend (busy or not)
// and for how much time it has been busy.
// If a backend is busy for too long, the watchdog will kill the process and
// force a reload of the model.
// The watchdog also supports LRU (Least Recently Used) eviction when a maximum
// number of active backends is configured.
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
}

type ProcessManager interface {
	ShutdownModel(modelName string) error
}

func NewWatchDog(pm ProcessManager, timeoutBusy, timeoutIdle time.Duration, busy, idle bool, lruLimit int) *WatchDog {
	return &WatchDog{
		timeout:         timeoutBusy,
		idletimeout:     timeoutIdle,
		pm:              pm,
		busyTime:        make(map[string]time.Time),
		idleTime:        make(map[string]time.Time),
		lastUsed:        make(map[string]time.Time),
		addressMap:      make(map[string]*process.Process),
		busyCheck:       busy,
		idleCheck:       idle,
		lruLimit:        lruLimit,
		addressModelMap: make(map[string]string),
		stop:            make(chan bool, 1),
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

func (wd *WatchDog) Shutdown() {
	wd.Lock()
	defer wd.Unlock()
	log.Info().Msg("[WatchDog] Shutting down watchdog")
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

// EnforceLRULimit ensures we're under the LRU limit by evicting least recently used models.
// This should be called before loading a new model.
// pendingLoads is the number of models currently being loaded (to account for concurrent loads).
// Returns the number of models evicted.
func (wd *WatchDog) EnforceLRULimit(pendingLoads int) int {
	if wd.lruLimit <= 0 {
		return 0 // LRU disabled
	}

	wd.Lock()

	currentCount := len(wd.addressModelMap)
	// We need to evict enough to make room for the new model AND any pending loads
	// Total after loading = currentCount + pendingLoads + 1 (the new one we're about to load)
	// We need: currentCount + pendingLoads + 1 <= lruLimit
	// So evict: currentCount + pendingLoads + 1 - lruLimit = currentCount - lruLimit + pendingLoads + 1
	modelsToEvict := currentCount - wd.lruLimit + pendingLoads + 1
	if modelsToEvict <= 0 {
		wd.Unlock()
		return 0
	}

	log.Debug().Int("current", currentCount).Int("pendingLoads", pendingLoads).Int("limit", wd.lruLimit).Int("toEvict", modelsToEvict).Msg("[WatchDog] LRU enforcement triggered")

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
	for i := 0; i < modelsToEvict && i < len(models); i++ {
		m := models[i]
		log.Info().Str("model", m.model).Time("lastUsed", m.lastUsed).Msg("[WatchDog] LRU evicting model")
		modelsToShutdown = append(modelsToShutdown, m.model)
		// Clean up the maps while we have the lock
		wd.untrack(m.address)
	}
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent deadlock
	for _, model := range modelsToShutdown {
		if err := wd.pm.ShutdownModel(model); err != nil {
			log.Error().Err(err).Str("model", model).Msg("[WatchDog] error shutting down model during LRU eviction")
		}
		log.Debug().Str("model", model).Msg("[WatchDog] LRU eviction complete")
	}

	return len(modelsToShutdown)
}

func (wd *WatchDog) Run() {
	log.Info().Msg("[WatchDog] starting watchdog")

	for {
		select {
		case <-wd.stop:
			log.Info().Msg("[WatchDog] Stopping watchdog")
			return
		case <-time.After(30 * time.Second):
			if !wd.busyCheck && !wd.idleCheck {
				log.Info().Msg("[WatchDog] No checks enabled, stopping watchdog")
				return
			}
			if wd.busyCheck {
				wd.checkBusy()
			}
			if wd.idleCheck {
				wd.checkIdle()
			}
		}
	}
}

func (wd *WatchDog) checkIdle() {
	wd.Lock()
	log.Debug().Msg("[WatchDog] Watchdog checks for idle connections")

	// Collect models to shutdown while holding the lock
	var modelsToShutdown []string
	for address, t := range wd.idleTime {
		log.Debug().Msgf("[WatchDog] %s: idle connection", address)
		if time.Since(t) > wd.idletimeout {
			log.Warn().Msgf("[WatchDog] Address %s is idle for too long, killing it", address)
			model, ok := wd.addressModelMap[address]
			if ok {
				modelsToShutdown = append(modelsToShutdown, model)
			} else {
				log.Warn().Msgf("[WatchDog] Address %s unresolvable", address)
			}
			wd.untrack(address)
		}
	}
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent deadlock
	for _, model := range modelsToShutdown {
		if err := wd.pm.ShutdownModel(model); err != nil {
			log.Error().Err(err).Str("model", model).Msg("[watchdog] error shutting down model")
		}
		log.Debug().Msgf("[WatchDog] model shut down: %s", model)
	}
}

func (wd *WatchDog) checkBusy() {
	wd.Lock()
	log.Debug().Msg("[WatchDog] Watchdog checks for busy connections")

	// Collect models to shutdown while holding the lock
	var modelsToShutdown []string
	for address, t := range wd.busyTime {
		log.Debug().Msgf("[WatchDog] %s: active connection", address)

		if time.Since(t) > wd.timeout {
			model, ok := wd.addressModelMap[address]
			if ok {
				log.Warn().Msgf("[WatchDog] Model %s is busy for too long, killing it", model)
				modelsToShutdown = append(modelsToShutdown, model)
			} else {
				log.Warn().Msgf("[WatchDog] Address %s unresolvable", address)
			}
			wd.untrack(address)
		}
	}
	wd.Unlock()

	// Now shutdown models without holding the watchdog lock to prevent deadlock
	for _, model := range modelsToShutdown {
		if err := wd.pm.ShutdownModel(model); err != nil {
			log.Error().Err(err).Str("model", model).Msg("[watchdog] error shutting down model")
		}
		log.Debug().Msgf("[WatchDog] model shut down: %s", model)
	}
}

func (wd *WatchDog) untrack(address string) {
	delete(wd.busyTime, address)
	delete(wd.idleTime, address)
	delete(wd.lastUsed, address)
	delete(wd.addressModelMap, address)
	delete(wd.addressMap, address)
}
