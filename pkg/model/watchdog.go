package model

import (
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
// force a reload of the model
// The watchdog runs as a separate go routine,
// and the GRPC client talks to it via a channel to send status updates
type WatchDog struct {
	sync.Mutex
	timetable            map[string]time.Time
	idleTime             map[string]time.Time
	timeout, idletimeout time.Duration
	addressMap           map[string]*process.Process
	addressModelMap      map[string]string
	pm                   ProcessManager
	stop                 chan bool

	busyCheck, idleCheck bool
}

type ProcessManager interface {
	ShutdownModel(modelName string) error
}

func NewWatchDog(pm ProcessManager, timeoutBusy, timeoutIdle time.Duration, busy, idle bool) *WatchDog {
	return &WatchDog{
		timeout:         timeoutBusy,
		idletimeout:     timeoutIdle,
		pm:              pm,
		timetable:       make(map[string]time.Time),
		idleTime:        make(map[string]time.Time),
		addressMap:      make(map[string]*process.Process),
		busyCheck:       busy,
		idleCheck:       idle,
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
	delete(wd.idleTime, address)
}

func (wd *WatchDog) UnMark(ModelAddress string) {
	wd.Lock()
	defer wd.Unlock()
	delete(wd.timetable, ModelAddress)
	wd.idleTime[ModelAddress] = time.Now()
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
	defer wd.Unlock()
	log.Debug().Msg("[WatchDog] Watchdog checks for idle connections")
	for address, t := range wd.idleTime {
		log.Debug().Msgf("[WatchDog] %s: idle connection", address)
		if time.Since(t) > wd.idletimeout {
			log.Warn().Msgf("[WatchDog] Address %s is idle for too long, killing it", address)
			model, ok := wd.addressModelMap[address]
			if ok {
				if err := wd.pm.ShutdownModel(model); err != nil {
					log.Error().Err(err).Str("model", model).Msg("[watchdog] error shutting down model")
				}
				log.Debug().Msgf("[WatchDog] model shut down: %s", address)
				delete(wd.idleTime, address)
				delete(wd.addressModelMap, address)
				delete(wd.addressMap, address)
			} else {
				log.Warn().Msgf("[WatchDog] Address %s unresolvable", address)
				delete(wd.idleTime, address)
			}
		}
	}
}

func (wd *WatchDog) checkBusy() {
	wd.Lock()
	defer wd.Unlock()
	log.Debug().Msg("[WatchDog] Watchdog checks for busy connections")

	for address, t := range wd.timetable {
		log.Debug().Msgf("[WatchDog] %s: active connection", address)

		if time.Since(t) > wd.timeout {

			model, ok := wd.addressModelMap[address]
			if ok {
				log.Warn().Msgf("[WatchDog] Model %s is busy for too long, killing it", model)
				if err := wd.pm.ShutdownModel(model); err != nil {
					log.Error().Err(err).Str("model", model).Msg("[watchdog] error shutting down model")
				}
				log.Debug().Msgf("[WatchDog] model shut down: %s", address)
				delete(wd.timetable, address)
				delete(wd.addressModelMap, address)
				delete(wd.addressMap, address)
			} else {
				log.Warn().Msgf("[WatchDog] Address %s unresolvable", address)
				delete(wd.timetable, address)
			}
		}
	}
}
