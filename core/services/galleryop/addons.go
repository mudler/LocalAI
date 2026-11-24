package galleryop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/xlog"
)

// AddonOpKeyPrefix is the OpCache key prefix of an add-on install, so the
// operations list can tell it from a model or a backend, and so an add-on
// never collides with a model or a backend of the same name.
const AddonOpKeyPrefix = "addon:"

// AddonOpKey is the OpCache key of the install of an add-on.
func AddonOpKey(addonID string) string { return AddonOpKeyPrefix + addonID }

// ParseAddonOpKey returns the add-on ID of a key built by AddonOpKey.
func ParseAddonOpKey(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, AddonOpKeyPrefix)
	return id, ok && id != ""
}

// AddonOp is one add-on install of the operation queue. The gallery service
// knows nothing of add-ons: the caller hands in what runs the install and what
// releases it when it never ran. The add-on owner keeps its own journal and
// its own serialization of lifecycle operations; the queue only orders the
// installs and runs one at a time.
type AddonOp struct {
	ID string
	// AddonID is the add-on, shown as the name of the operation.
	AddonID string
	// Upgrade is an upgrade of an installed add-on, not an install.
	Upgrade bool
	// InRun is true for an upgrade that an upgrade run started
	// (ManagementOp.InRun).
	InRun bool
	// Context is the operation context; CancelFunc cancels it. EnqueueAddonOp
	// makes them when Context is nil.
	Context    context.Context
	CancelFunc context.CancelFunc
	// Run installs the add-on. It reports its phases through progress, which
	// sets the operation status; a status it reports with Cancellable false
	// makes a cancel refused from then on.
	Run func(ctx context.Context, progress func(*OpStatus)) error
	// Abandon releases the owner's record of an install that never ran (a
	// cancel while queued, or the service stopping). Optional.
	Abandon func()
}

// EnqueueAddonOp admits an add-on install: like EnqueueModelOp it publishes a
// "queued" status at once and never blocks the caller. The install runs on the
// backend operation scheduler, so it shares its worker pool, its limit and its
// cancel path with backend operations. Its key is the add-on, so the same
// add-on never installs twice at once; the add-on owner runs one lifecycle
// operation at a time anyway. A cancel while queued drops the install and
// releases it.
func (g *GalleryService) EnqueueAddonOp(op AddonOp) {
	if op.Context == nil {
		var cancel context.CancelFunc
		op.Context, cancel, _ = NewUserCancellableContext(context.Background())
		op.CancelFunc = cancel
	}
	if op.CancelFunc != nil {
		g.storeCancellation(op.ID, op.CancelFunc, op.CancelFunc)
	}
	g.markQueued(op.ID, op.AddonID, false, op.Upgrade)
	abandon := func() {
		g.abandonQueued(op.ID, op.AddonID)
		g.abandonAddonOp(op)
	}
	sched := g.backendScheduler
	if sched == nil {
		// The service has not started: nothing would ever run it.
		abandon()
		return
	}
	sched.submit(&opTask{
		key:     AddonOpKey(op.AddonID),
		ctx:     op.Context,
		run:     func() { g.runAddonOp(op) },
		abandon: abandon,
	})
}

// abandonAddonOp releases an install that the worker will not run.
func (g *GalleryService) abandonAddonOp(op AddonOp) {
	g.removeCancellation(op.ID)
	if op.Abandon != nil {
		op.Abandon()
	}
}

// failAddonOp writes the failure of an install the way Start's updateError
// does for models and backends, including the opaque errors setting.
func (g *GalleryService) failAddonOp(id string, err error) {
	xlog.Error("Gallery operation failed", "op_id", id, "error", err)
	if g.appConfig != nil && g.appConfig.OpaqueErrors {
		g.UpdateStatus(id, &OpStatus{Error: fmt.Errorf("an error occurred"), Processed: true})
		return
	}
	g.UpdateStatus(id, &OpStatus{Error: err, Processed: true, Message: "error: " + err.Error()})
}

func (g *GalleryService) runAddonOp(op AddonOp) {
	// The send and a cancel can be ready together; a cancelled op never runs.
	if op.Context.Err() != nil {
		g.abandonQueued(op.ID, op.AddonID)
		g.abandonAddonOp(op)
		return
	}
	g.Lock()
	store := g.galleryStore
	g.Unlock()
	if store != nil {
		if err := store.Create(&distributed.GalleryOperationRecord{
			ID: op.ID, GalleryElementName: op.AddonID, OpType: addonOpType(op.Upgrade), Status: "pending", Cancellable: true,
		}); err != nil {
			xlog.Warn("Failed to create gallery operation record", "op_id", op.ID, "error", err)
		}
	}
	g.UpdateStatus(op.ID, &OpStatus{Message: "starting", GalleryElementName: op.AddonID, Cancellable: true})
	err := runOpHandler(func() error {
		return op.Run(op.Context, func(st *OpStatus) {
			if st == nil {
				return
			}
			st.GalleryElementName = op.AddonID
			// A stop is refused once the install says it can no longer stop;
			// the cancel callback is dropped with it.
			if !st.Cancellable {
				g.removeCancellation(op.ID)
			}
			g.UpdateStatus(op.ID, st)
		})
	})
	switch {
	case err != nil && op.Context.Err() != nil:
		// stopOperation already wrote the cancelled status.
		g.UpdateStatus(op.ID, &OpStatus{Processed: true, Cancelled: true, Message: stopWord(false), GalleryElementName: op.AddonID})
	case err != nil:
		g.failAddonOp(op.ID, err)
	default:
		g.UpdateStatus(op.ID, &OpStatus{Processed: true, Progress: 100, Message: MessageCompleted, GalleryElementName: op.AddonID})
	}
	g.announceAddonOp(op.ID, op.AddonID, op.Upgrade, op.InRun, err)
	g.removeCancellation(op.ID)
}

// announceAddonOp is announceOp for an add-on install.
func (g *GalleryService) announceAddonOp(id, name string, upgrade, inRun bool, err error) {
	g.Lock()
	cb := g.OnOpFinished
	st := g.statuses[id]
	g.Unlock()
	if cb == nil || id == "" {
		return
	}
	res := OpResult{ID: id, Name: name, Addon: true, Upgrade: upgrade, InRun: inRun}
	switch {
	case st != nil && st.Cancelled:
		res.Outcome = OpCancelled
	case err == nil:
		res.Outcome = OpCompleted
	default:
		res.Outcome = OpFailed
		res.Err = err.Error()
		res.NeedsApproval = IsAddonAttention(err)
	}
	go cb(res)
}

// AddonAttentionError is the failure of an add-on install that waits for the
// administrator: the verified package asks for host access the administrator
// did not review. It is not retried by itself.
type AddonAttentionError struct{ Reason string }

// addonAttentionPrefix starts the text of an AddonAttentionError. A status
// replicated from another frontend carries the text only, so the prefix is
// what IsAddonAttention reads there.
const addonAttentionPrefix = "the add-on needs your approval again"

func (e *AddonAttentionError) Error() string {
	return fmt.Sprintf("%s: %s", addonAttentionPrefix, e.Reason)
}

// IsAddonAttention reports whether a failed add-on install waits for a new
// review of its host access.
func IsAddonAttention(err error) bool {
	var a *AddonAttentionError
	return errors.As(err, &a) || (err != nil && strings.HasPrefix(err.Error(), addonAttentionPrefix))
}

// addonOpType is the operation type of the record in the gallery store.
func addonOpType(upgrade bool) string {
	if upgrade {
		return "addon_upgrade"
	}
	return "addon_install"
}
