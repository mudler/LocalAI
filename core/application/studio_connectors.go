package application

import (
	"context"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// telegramBase is the Bot API address; only a test changes it.
var telegramBase string

// UseTelegramBaseURLForTests points the connectors of applications built after
// the call at another Bot API address, and returns a function that restores the
// real one. It exists so an end-to-end test can use a fake Bot API.
func UseTelegramBaseURLForTests(base string) (restore func()) {
	prev := telegramBase
	telegramBase = base
	return func() { telegramBase = prev }
}

// StudioConnectors returns the connector manager of the chats, or nil.
func (a *Application) StudioConnectors() *studio.Connectors { return a.studioConnectors }

// startStudioConnectors builds the connector manager and starts its loop. In a
// cluster the polling runs on the one frontend that holds the advisory lock.
func (a *Application) startStudioConnectors(svc *studio.Service) {
	if a.studioConnStore == nil {
		return
	}
	opts := studio.ConnectorOptions{Store: a.studioConnStore, Telegram: &connectors.Telegram{BaseURL: telegramBase}, Poll: 25}
	if a.Distributed() != nil && a.authDB != nil {
		lock := advisorylock.NewHeldLock(a.authDB, advisorylock.KeyChatsConnectors)
		opts.Lead = func(ctx context.Context) bool {
			if lock.Held() {
				return lock.Verify(ctx)
			}
			ok, err := lock.TryAcquire(ctx)
			if err != nil {
				xlog.Warn("Connectors: could not try the leader lock", "error", err)
			}
			return ok
		}
	}
	c, err := studio.NewConnectors(svc, opts)
	if err != nil {
		xlog.Warn("Connectors disabled: failed to start", "error", err)
		return
	}
	svc.SetConnectors(c)
	ctx := a.applicationConfig.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	a.studioConnectors, a.studioConnectorsStop = c, cancel
	go c.Run(ctx)
}
