package galleryop

import (
	"context"
	"strings"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/distributed"
)

// PluginOpKeyPrefix is the OpCache key prefix of a plugin install or update,
// so the operations list can tell it from a model, a backend or an add-on.
const PluginOpKeyPrefix = "plugin:"

// PluginOpKey is the OpCache key of a change of a plugin.
func PluginOpKey(pluginID string) string { return PluginOpKeyPrefix + pluginID }

// ParsePluginOpKey returns the plugin ID of a key built by PluginOpKey.
func ParsePluginOpKey(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, PluginOpKeyPrefix)
	return id, ok && id != ""
}

// PluginOp is one plugin install or update of the operation queue. As for
// add-ons, the gallery service knows nothing of plugins: the Plugins service
// hands in what runs the change. The job runs on the frontend that took the
// request (spec 7.4); a restart fails it and never resumes it.
type PluginOp struct {
	ID string
	// PluginID is the plugin (name@marketplace), shown as the name of the
	// operation.
	PluginID string
	// Update is an update of an installed plugin, not an install.
	Update bool
	// By is the user who started the change; the end of the change is told
	// to them.
	By string
	// Context is the operation context; CancelFunc cancels it.
	// EnqueuePluginOp makes them when Context is nil.
	Context    context.Context
	CancelFunc context.CancelFunc
	Run        func(ctx context.Context, progress func(*OpStatus)) error
	// Abandon releases what the caller holds for a change that never ran (a
	// cancel while queued, or the service stopping). Optional.
	Abandon func()
}

// EnqueuePluginOp admits a plugin change: it publishes a "queued" status at
// once and never blocks the caller. It runs on the backend operation
// scheduler, keyed by the plugin ID, so one plugin ID never changes twice at
// once on this frontend. A cancel while queued drops it and writes nothing.
func (g *GalleryService) EnqueuePluginOp(op PluginOp) {
	if op.Context == nil {
		var cancel context.CancelFunc
		op.Context, cancel, _ = NewUserCancellableContext(context.Background())
		op.CancelFunc = cancel
	}
	if op.CancelFunc != nil {
		g.storeCancellation(op.ID, op.CancelFunc, op.CancelFunc)
	}
	g.markQueued(op.ID, op.PluginID, false, op.Update)
	abandon := func() {
		g.abandonQueued(op.ID, op.PluginID)
		g.removeCancellation(op.ID)
		if op.Abandon != nil {
			op.Abandon()
		}
	}
	sched := g.backendScheduler
	if sched == nil {
		abandon()
		return
	}
	sched.submit(&opTask{
		key:     PluginOpKey(op.PluginID),
		ctx:     op.Context,
		run:     func() { g.runPluginOp(op, abandon) },
		abandon: abandon,
	})
}

func (g *GalleryService) runPluginOp(op PluginOp, abandon func()) {
	if op.Context.Err() != nil {
		abandon()
		return
	}
	g.Lock()
	store := g.galleryStore
	g.Unlock()
	opType := "plugin_install"
	if op.Update {
		opType = "plugin_update"
	}
	if store != nil {
		if err := store.Create(&distributed.GalleryOperationRecord{
			ID: op.ID, GalleryElementName: op.PluginID, OpType: opType, Status: "pending", Cancellable: true,
		}); err != nil {
			xlog.Warn("Failed to create gallery operation record", "op_id", op.ID, "error", err)
		}
	}
	g.UpdateStatus(op.ID, &OpStatus{Message: "starting", GalleryElementName: op.PluginID, Cancellable: true, Upgrade: op.Update})
	err := runOpHandler(func() error {
		return op.Run(op.Context, func(st *OpStatus) {
			if st == nil {
				return
			}
			st.GalleryElementName = op.PluginID
			st.Upgrade = op.Update
			if !st.Cancellable {
				g.removeCancellation(op.ID)
			}
			g.UpdateStatus(op.ID, st)
		})
	})
	switch {
	case err != nil && op.Context.Err() != nil:
		g.UpdateStatus(op.ID, &OpStatus{Processed: true, Cancelled: true, Message: stopWord(false), GalleryElementName: op.PluginID, Upgrade: op.Update})
	case err != nil:
		g.failAddonOp(op.ID, err)
	default:
		g.UpdateStatus(op.ID, &OpStatus{Processed: true, Progress: 100, Message: MessageCompleted, GalleryElementName: op.PluginID, Upgrade: op.Update})
	}
	g.announcePluginOp(op, err)
	g.removeCancellation(op.ID)
}

// announcePluginOp is announceOp for a plugin install or update: the person
// who started it learns how it ended, which a user cannot read from the
// operations list.
func (g *GalleryService) announcePluginOp(op PluginOp, err error) {
	g.Lock()
	cb := g.OnOpFinished
	st := g.statuses[op.ID]
	g.Unlock()
	if cb == nil || op.ID == "" {
		return
	}
	res := OpResult{ID: op.ID, Name: op.PluginID, Plugin: true, Upgrade: op.Update, By: op.By}
	switch {
	case st != nil && st.Cancelled:
		res.Outcome = OpCancelled
	case err == nil:
		res.Outcome = OpCompleted
	default:
		res.Outcome = OpFailed
		res.Err = err.Error()
	}
	go cb(res)
}
