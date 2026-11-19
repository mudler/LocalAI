package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/notify"
)

// Notifier returns the notification center, or nil when its store could not
// start. It does not depend on the chats: the update center, the operations
// and the chats all write to it, and the bell reads it, with the chats on or
// off.
func (a *Application) Notifier() *notify.Service { return a.notifier }

// NotifyPusher returns the browser push service of the notification center, or
// nil when there is nowhere to keep subscriptions (no database and no data
// path).
func (a *Application) NotifyPusher() *notify.Pusher { return a.notifyPusher }

// initNotifications builds the notification center: the store, the browser
// push, and the fan-out that tells the other frontends of a cluster. It runs
// after the distributed wiring (the fan-out needs the message bus) and before
// the chats service, which takes the notifier it builds.
//
// The store is the one the chats always used: GORM on the auth database when
// there is one (a cluster always has it), JSON files under
// <data path>/chats/notifications otherwise, memory when there is no data
// path. The folder keeps its old name on purpose, so an upgrade moves nothing.
// A store that cannot start leaves the notifier nil: nothing is written, and
// nothing else stops.
func (a *Application) initNotifications() {
	var (
		st   notify.Store
		ps   notify.PushStore
		kind string
		err  error
	)
	switch {
	case a.authDB != nil:
		kind = "gorm"
		if st, err = notify.NewGORM(a.authDB); err == nil {
			ps, err = notify.NewGORMPush(a.authDB)
		}
	case a.applicationConfig.DataPath != "":
		kind = "json"
		dir := filepath.Join(a.applicationConfig.DataPath, "chats")
		if st, err = notify.NewJSON(filepath.Join(dir, "notifications")); err == nil {
			ps, err = notify.NewJSONPush(filepath.Join(dir, "push"))
		}
	default:
		// Memory keeps nothing across a restart, so there is nowhere to keep
		// push subscriptions either.
		kind = "memory"
		st, err = notify.NewJSON("")
	}
	if err != nil {
		xlog.Warn("Notifications disabled: failed to initialise their store", "kind", kind, "error", err)
		return
	}
	var fan notify.Fanout
	if d := a.Distributed(); d != nil && d.Nats != nil {
		fan = notify.NewBroadcastFanout(d.Nats, nil)
	}
	n := notify.NewService(notify.Options{Store: st, Fanout: fan})
	// A notification of a run goes to the Telegram chat of a binding too. The
	// connector manager is set later and only when the chats are on, so the
	// hook looks it up when it runs.
	n.OnAdd(func(item notify.Notification) {
		if c := a.studioConnectorsHook.Load(); c != nil {
			c.Forward(context.Background(), item)
		}
	})
	if ps != nil {
		pusher := notify.NewPusher(notify.PusherOptions{Store: ps})
		// The frontend that stores a notification pushes it, so a notification
		// is pushed once in a cluster.
		n.OnAdd(pusher.SendAsync)
		a.notifyPusher = pusher
	}
	a.notifier = n
	xlog.Debug("Notifications ready", "store", kind, "cluster", fan != nil)
}

// announceOperations tells the administrators when a model or backend
// install, removal or upgrade they started reaches its end. The gallery
// service fires the hook on the frontend that ran the operation, and the item
// is keyed by the operation ID, so it is written once. Without a notification
// center nothing is written.
func (a *Application) announceOperations() {
	if a.galleryService == nil {
		return
	}
	em := a.updateEmitter()
	a.galleryService.SetOnOpFinished(func(r galleryop.OpResult) {
		if err := em.Operation(context.Background(), r); err != nil {
			xlog.Warn("Could not write the notification of an operation", "operation", r.ID, "error", err)
		}
	})
}
