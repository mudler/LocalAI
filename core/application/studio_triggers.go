package application

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// StudioTriggers returns the trigger host of the chats, or nil when it could
// not be built.
func (a *Application) StudioTriggers() *studio.Triggers { return a.studioTriggers }

// initStudioTriggerStore opens the store of the triggers, beside the chats:
// GORM on the auth database, JSON files under the data path, or memory.
func (a *Application) initStudioTriggerStore() error {
	var (
		st  triggers.Store
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = triggers.NewGORM(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = triggers.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats", "triggers"))
	default:
		st, err = triggers.NewJSON("")
	}
	if err != nil {
		return err
	}
	a.studioTriggerStore = st
	return nil
}

// startStudioTriggers builds the trigger host and starts its loop. Triggers
// are stored beside the chats: GORM on the auth database, JSON files under the
// data path, or memory. A cluster runs the Runner on one frontend at a time:
// the one that holds the advisory lock, which it keeps until it stops or loses
// its database session.
func (a *Application) startStudioTriggers(svc *studio.Service) {
	if a.applicationConfig.DisableTriggers {
		xlog.Info("Triggers are turned off (LOCALAI_DISABLE_TRIGGERS)")
		return
	}
	st := a.studioTriggerStore
	if st == nil {
		return
	}
	opts := studio.TriggerOptions{Store: st, Retention: a.triggerRunRetention()}
	if a.Distributed() != nil && a.authDB != nil {
		opts.Lead = a.studioLeader(advisorylock.KeyChatsTriggers, "Triggers")
	}
	host, err := studio.NewTriggers(svc, opts)
	if err != nil {
		xlog.Warn("Triggers disabled: failed to start", "error", err)
		return
	}
	svc.SetTriggers(host)
	ctx := a.backgroundContext()
	ctx, cancel := context.WithCancel(ctx)
	a.studioTriggers, a.studioTriggersStop = host, cancel
	go host.Run(ctx)
}

// triggerRunRetention turns the operator's settings into what the Runner
// takes. A negative number of days keeps the runs forever.
func (a *Application) triggerRunRetention() triggers.RunRetention {
	cfg := a.applicationConfig
	ret := triggers.RunRetention{Keep: cfg.TriggerRunKeep}
	if cfg.TriggerRunRetentionDays != 0 {
		ret.MaxAge = time.Duration(cfg.TriggerRunRetentionDays) * 24 * time.Hour
	}
	return ret
}
