package application

import (
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
)

// initStudioCoordinatorStore opens the store of the coordinator agents, beside
// the chats: GORM on the auth database, JSON files under the data path, or
// memory.
func (a *Application) initStudioCoordinatorStore() error {
	var (
		st  coordinator.Store
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = coordinator.NewGORM(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = coordinator.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats", "coordinators"))
	default:
		st, err = coordinator.NewJSON("")
	}
	if err != nil {
		return err
	}
	a.studioCoordinatorStore = st
	return nil
}

// startStudioCoordinators builds the coordinator host. The host has no loop of
// its own: its wake-ups run in the step of the reaper, on the frontend that
// holds the reaper lock.
func (a *Application) startStudioCoordinators(svc *studio.Service) {
	st := a.studioCoordinatorStore
	if st == nil {
		return
	}
	cfg := a.applicationConfig
	host, err := studio.NewCoordinators(svc, studio.CoordinatorOptions{
		Store: st,
		Limits: func() studio.CoordinatorLimits {
			return studio.CoordinatorLimits{
				HelpersCeiling: cfg.EffectiveChatsCoordinatorHelpersCeiling(),
				DepthCeiling:   cfg.EffectiveChatsCoordinatorDepthCeiling(),
				RunsPerDay:     cfg.EffectiveChatsCoordinatorRunsPerDay(),
				MaxRunning:     cfg.EffectiveChatsCoordinatorMaxRunning(),
				AuditDays:      cfg.EffectiveChatsCoordinatorAuditDays(),
			}
		},
		RunLimit: cfg.EffectiveChatsTriggerRunLimit,
	})
	if err != nil {
		xlog.Warn("Coordinators disabled: failed to start", "error", err)
		return
	}
	svc.SetCoordinators(host)
	a.studioCoordinators = host
}
