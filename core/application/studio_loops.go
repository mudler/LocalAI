package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
)

// initStudioLoopStore opens the store of the loops of the chats, beside the
// chats: GORM on the auth database, JSON files under the data path, or memory.
func (a *Application) initStudioLoopStore() error {
	var (
		st  loops.Store
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = loops.NewGORM(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = loops.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats", "loops"))
	default:
		st, err = loops.NewJSON("")
	}
	if err != nil {
		return err
	}
	a.studioLoopStore = st
	return nil
}

// startStudioLoops builds the loop host and starts it. A cluster fires the
// loops on one frontend at a time: the one that holds the advisory lock.
func (a *Application) startStudioLoops(svc *studio.Service) {
	st := a.studioLoopStore
	if st == nil {
		return
	}
	cfg := a.applicationConfig
	opts := studio.LoopOptions{Store: st, Limits: func() studio.LoopLimits {
		return studio.LoopLimits{
			MinInterval: cfg.EffectiveChatsLoopMinInterval(), MaxRuns: cfg.EffectiveChatsLoopMaxRuns(),
			PerChat: cfg.EffectiveChatsLoopMaxPerChat(), PerUser: cfg.EffectiveChatsLoopMaxPerUser(),
			DailyRuns: cfg.EffectiveChatsLoopDailyRuns(),
		}
	}}
	if a.Distributed() != nil && a.authDB != nil {
		opts.Lead = a.studioLeader(advisorylock.KeyChatsLoops, "Loops")
	}
	host, err := studio.NewLoops(svc, opts)
	if err != nil {
		xlog.Warn("Loops disabled: failed to start", "error", err)
		return
	}
	svc.SetLoops(host)
	ctx, cancel := context.WithCancel(a.backgroundContext())
	a.studioLoops, a.studioLoopsStop = host, cancel
	go host.Run(ctx)
}
