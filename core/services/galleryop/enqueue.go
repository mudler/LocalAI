package galleryop

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/xlog"
)

// queuedMessage is the status message an operation carries between admission
// (the HTTP handler minting the job ID) and the moment the worker starts it.
const queuedMessage = "queued"

// EnqueueModelOp admits a model operation: it registers a queryable "queued"
// status for op.ID and then hands the op to the gallery worker.
//
// Why the status is written here and not in modelHandler: the gallery channels
// are unbuffered and the worker processes one operation at a time, so an op
// submitted while another install is downloading sits in a blocked send for as
// long as that install takes. The admission handlers return HTTP 200 with the
// job ID immediately, so for that whole window the client held an ID that
// GET /models/jobs/<id> answered with "could not find any status for ID" and
// GET /models/jobs did not list at all — the endpoint reported success for work
// nothing could observe. Writing the status before the send makes the job
// queryable from the instant its ID is handed out.
//
// Delivery is asynchronous on purpose: a direct send would block the HTTP
// handler for the entire duration of the in-flight install (observed as a
// request that never gets a response while /readyz stays green).
func (g *GalleryService) EnqueueModelOp(op ManagementOp[gallery.GalleryModel, gallery.ModelConfig]) {
	g.markQueued(op.ID, op.GalleryElementName, op.Delete)
	go func() {
		select {
		case g.ModelGalleryChannel <- op:
		case <-enqueueContext(op.Context).Done():
			g.abandonQueued(op.ID, op.GalleryElementName)
		}
	}()
}

// EnqueueBackendOp is the BackendGalleryChannel sibling of EnqueueModelOp.
// Same rationale — see that comment.
func (g *GalleryService) EnqueueBackendOp(op ManagementOp[gallery.GalleryBackend, any]) {
	g.markQueued(op.ID, op.GalleryElementName, op.Delete)
	go func() {
		select {
		case g.BackendGalleryChannel <- op:
		case <-enqueueContext(op.Context).Done():
			g.abandonQueued(op.ID, op.GalleryElementName)
		}
	}()
}

// enqueueContext gives the delivery goroutine something to select on. Ops
// submitted without a context (the /models/apply path) simply wait for the
// worker; ops that carry one (every UI path) are released the moment the
// operation is cancelled, so cancelling a still-queued op no longer strands
// the goroutine on a send that will never be received.
func enqueueContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// markQueued publishes the pre-worker status for an admitted operation.
func (g *GalleryService) markQueued(id, elementName string, deletion bool) {
	if id == "" {
		return
	}
	g.UpdateStatus(id, &OpStatus{
		Message:            queuedMessage,
		Phase:              queuedMessage,
		GalleryElementName: elementName,
		Deletion:           deletion,
		Cancellable:        !deletion,
	})
}

// abandonQueued turns a queued operation that the worker never accepted into a
// terminal failure. Without it the op keeps claiming it is waiting to start
// while the goroutine that was supposed to deliver it is gone, which is the
// same phantom-operation shape as an op orphaned by a replica restart.
//
// A status that already reached a terminal state (a concurrent cancel is the
// common case) wins: we must not overwrite "cancelled" with a generic failure.
func (g *GalleryService) abandonQueued(id, elementName string) {
	if id == "" {
		return
	}
	g.Lock()
	if st, ok := g.statuses[id]; ok && st.Processed {
		g.Unlock()
		return
	}
	g.Unlock()

	xlog.Warn("Gallery operation was never picked up by the worker", "op_id", id, "element", elementName)
	g.UpdateStatus(id, &OpStatus{
		Processed:          true,
		Error:              fmt.Errorf("operation was cancelled before the gallery worker could start it"),
		Message:            "error: operation was cancelled before the gallery worker could start it",
		GalleryElementName: elementName,
	})
}
