package model

import (
	"sync"
	"time"

	process "github.com/mudler/go-processmanager"
	"github.com/rs/zerolog/log"
)

// All GRPC Clients created by ModelLoader should have an associated injected
// watchdog that will keep track of the state of each backend (busy or not)
// and for how much time it has been busy.
// If a backend is busy for too long, the watchdog will kill the process and
// force a reload of the model
// The watchdog runs as a separate go routine,
// and the GRPC client talks to it via a channel to send status updates

type WatchDog struct {
	sync.Mutex
	timetable       map[string]time.Time
	timeout         time.Duration
	addressMap      map[string]*process.Process
	addressModelMap map[string]string
	pm              ProcessManager
	stop            chan bool
}

type ProcessManager interface {
	ShutdownModel(modelName string) error
}

func NewWatchDog(timeout time.Duration, pm ProcessManager) *WatchDog {
	return &WatchDog{
		timeout:         timeout,
		pm:              pm,
		timetable:       make(map[string]time.Time),
		addressMap:      make(map[string]*process.Process),
		addressModelMap: make(map[string]string),
	}
}

func (wd *WatchDog) Shutdown() {
	wd.Lock()
	defer wd.Unlock()
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
	wd.timetable[address] = time.Now()
}

func (wd *WatchDog) UnMark(ModelAddress string) {
	wd.Lock()
	defer wd.Unlock()
	delete(wd.timetable, ModelAddress)
}

func (wd *WatchDog) Run() {
	log.Info().Msg("[WatchDog] starting watchdog")

	for {
		select {
		case <-wd.stop:
			log.Info().Msg("[WatchDog] Stopping watchdog")
			return
		case <-time.After(5 * time.Second):
			log.Debug().Msg("[WatchDog] Watchdog checks for stale backends")
			wd.checkBusy()
		}
	}
}

func (wd *WatchDog) checkBusy() {
	wd.Lock()
	defer wd.Unlock()
	for address, t := range wd.timetable {
		log.Debug().Msgf("[WatchDog] %s: active connection", address)

		if time.Since(t) > wd.timeout {

			model, ok := wd.addressModelMap[address]
			if ok {
				log.Warn().Msgf("[WatchDog] Model %s is busy for too long, killing it", model)
				if err := wd.pm.ShutdownModel(model); err != nil {
					log.Error().Msgf("[watchdog] Error shutting down model %s: %v", model, err)
				}
				delete(wd.timetable, address)
				delete(wd.addressModelMap, address)
				delete(wd.addressMap, address)
			} else {
				log.Warn().Msgf("[WatchDog] Address %s unresolvable", address)
			}

		}
	}
}
