// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// maxQueueItems bounds the installs of one queue request.
const maxQueueItems = 32

// hostGrantsConfirmQueue is the value of the confirm header of a queue request.
const hostGrantsConfirmQueue = "queue"

// WithOperationQueue sets the operation queue that runs the approved installs
// in the background. opcache may be nil: the installs then run without a row
// in /api/operations.
func WithOperationQueue(gs *galleryop.GalleryService, opcache *galleryop.OpCache) Option {
	return func(s *settings) { s.queue, s.opcache = gs, opcache }
}

// HostGrantsQueueBody is the body of POST /api/addons/host-grants/queue.
type HostGrantsQueueBody struct {
	Items []gateway.HostGrantsQueueRequest `json:"items"`
}

// HostGrantsQueueItem is the answer for one install of a queue request: the
// job of /api/operations that runs it, or the refusal of the owner.
type HostGrantsQueueItem struct {
	AddonID     string      `json:"addonId"`
	OperationID string      `json:"operationId,omitempty"`
	JobID       string      `json:"jobId,omitempty"`
	Error       *AddonError `json:"error,omitempty"`
}

// HostGrantsQueueResult is the response of POST /api/addons/host-grants/queue.
type HostGrantsQueueResult struct {
	Items []HostGrantsQueueItem `json:"items"`
}

// QueueWithHostGrants queues approved installs.
// @Summary Queue add-on installs with the host access the administrator approved (browser session)
// @Description Administrators only, from their own browser session (same-origin fetch, X-LocalAI-Confirm equal to "queue"). API keys, bearer tokens, MCP sessions and tool calls are refused with 403 not_human_surface. Each item carries what host-grants/install carries for an install from a library: the release, and the approval of its preview (digest, ticked operations, grants revision and the preview nonce). The owner checks each approval now and records it; the install then runs in the background, in order, on the queue of backend operations and within its limit, as an operation of /api/operations (the owner installs one add-on at a time), with its phase and its downloaded bytes. Cancel it there (POST /api/operations/{jobID}/cancel) while it waits or downloads. The install never turns the add-on on. When the verified package asks for host access that the preview did not show, the install stops and asks for a new review (the operation fails and a notification says so); nothing is granted. Items are answered one by one: a refused item carries the error of host-grants/install and does not stop the others. Queued installs that have not finished when the owner restarts are failed with the code interrupted.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body HostGrantsQueueBody true "Installs to queue"
// @Success 200 {object} HostGrantsQueueResult "One answer per item"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface (not_human_surface) or administration denied"
// @Failure 503 {object} AddonErrorResponse "Addons or the operation queue are unavailable"
// @Router /api/addons/host-grants/queue [post]
func QueueWithHostGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		var body HostGrantsQueueBody
		if decode(c, &body) != nil || len(body.Items) == 0 || len(body.Items) > maxQueueItems {
			return failure(c, 400, "Invalid host access request")
		}
		noteRequest(c, contract.Request{Action: "hostgrants-" + gateway.HostGrantsQueue})
		if !humanSurface(c, s, a, hostGrantsConfirmQueue) {
			return c.JSON(403, AddonErrorResponse{Error: AddonError{Type: gateway.HostGrantsNotHumanSurface, Code: 403, Message: "This action requires your own browser session"}})
		}
		if g == nil || s.queue == nil {
			return failure(c, 503, "Addons unavailable")
		}
		ctx := gateway.WithHumanSurface(c.Request().Context())
		out := HostGrantsQueueResult{Items: make([]HostGrantsQueueItem, 0, len(body.Items))}
		for _, item := range body.Items {
			out.Items = append(out.Items, queueOne(ctx, g, s, a, item))
		}
		return c.JSON(200, out)
	}
}

// queueOne records one approved install on the owner and hands it to the
// operation queue.
func queueOne(ctx context.Context, g *gateway.Gateway, s settings, a contract.Actor, item gateway.HostGrantsQueueRequest) HostGrantsQueueItem {
	res := HostGrantsQueueItem{AddonID: item.AddonID}
	if item.OperationID == "" {
		id, e := newHostGrantsOperationID()
		if e != nil {
			res.Error = &AddonError{Type: "addon_error", Code: 503, Message: "Addons unavailable"}
			return res
		}
		item.OperationID = id
	}
	if !validOperationID(item.OperationID) {
		res.Error = &AddonError{Type: gateway.HostGrantsInvalidRequest, Code: 400, Message: "Invalid operationId"}
		return res
	}
	res.OperationID = item.OperationID
	raw, e := json.Marshal(item)
	if e != nil {
		res.Error = &AddonError{Type: gateway.HostGrantsInvalidRequest, Code: 400, Message: "Invalid host access request"}
		return res
	}
	if _, e = g.HostGrants(ctx, a, gateway.HostGrantsCall{Op: gateway.HostGrantsQueue, OperationID: item.OperationID, AddonID: item.AddonID, Body: raw}); e != nil {
		res.Error = queueError(e)
		return res
	}
	jobID := uuid.NewString()
	opID := item.OperationID
	call := func(op string) gateway.HostGrantsCall {
		b, _ := json.Marshal(gateway.HostGrantsQueuedRequest{OperationID: opID})
		return gateway.HostGrantsCall{Op: op, OperationID: opID, AddonID: item.AddonID, Body: b}
	}
	if s.opcache != nil {
		s.opcache.Set(galleryop.AddonOpKey(item.AddonID), jobID)
	}
	s.queue.EnqueueAddonOp(galleryop.AddonOp{
		ID: jobID, AddonID: item.AddonID,
		Run: func(ctx context.Context, progress func(*galleryop.OpStatus)) error {
			ctx = managerpkg.WithInstallProgress(ctx, func(p managerpkg.InstallProgress) { progress(installStatus(p)) })
			raw, err := g.HostGrants(ctx, a, call(gateway.HostGrantsRunQueued))
			if err != nil {
				return runError(err)
			}
			var st managerpkg.OperationStatus
			if json.Unmarshal(raw, &st) == nil && st.Phase == "failed" {
				return fmt.Errorf("the install failed (%s)", st.FailureCode)
			}
			return nil
		},
		// Releases the owner's record so the add-on is not blocked by an
		// install that will never run. The request that queued it is gone,
		// so this runs on its own context.
		Abandon: func() {
			_, _ = g.HostGrants(context.Background(), a, call(gateway.HostGrantsDropQueued))
		},
	})
	res.JobID = jobID
	return res
}

// installStatus is the operation status of one report of the owner.
func installStatus(p managerpkg.InstallProgress) *galleryop.OpStatus {
	st := &galleryop.OpStatus{Phase: p.Phase, Message: p.Phase, Cancellable: p.Cancellable, CurrentBytes: p.Bytes, TotalBytes: p.Total}
	if p.Total > 0 {
		st.Progress = min(100, float64(p.Bytes)*100/float64(p.Total))
		st.DownloadedFileSize = formatBytes(p.Bytes)
		st.TotalFileSize = formatBytes(p.Total)
	}
	return st
}

// queueError is the answer for an item the owner refused.
func queueError(e error) *AddonError {
	var he *gateway.HostGrantsError
	if errors.As(e, &he) {
		return &AddonError{Type: he.Type, Code: he.Status, Message: he.Message}
	}
	if errors.Is(e, managerpkg.ErrConflict) {
		return &AddonError{Type: "addon_error", Code: 409, Message: "The add-on is installed already or another operation on it is in progress"}
	}
	return &AddonError{Type: "addon_error", Code: 503, Message: "Addons unavailable"}
}

// runError is the error an install run reports to the operation queue. A
// package that asks for more host access than was reviewed waits for the
// administrator; the text says what to do.
func runError(e error) error {
	var he *gateway.HostGrantsError
	if errors.As(e, &he) {
		switch he.Type {
		case gateway.HostGrantsApprovalStale, gateway.HostGrantsApprovalRequired:
			return &galleryop.AddonAttentionError{Reason: "the package asks for host access you did not review; review it and install again"}
		}
		return errors.New(he.Message)
	}
	var cause *managerpkg.FailureCause
	if errors.As(e, &cause) {
		return fmt.Errorf("the install failed (%s)", cause.Code)
	}
	if errors.Is(e, managerpkg.ErrOperationFailed) {
		return errors.New("the install failed")
	}
	return e
}

// formatBytes is the size as the operations list shows it: "12.3 MB".
func formatBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
