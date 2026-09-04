package jobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"
)

// DefaultTerminalRecheck is how often an open job-progress SSE stream re-reads
// the job row while it waits for the broadcast that would close it.
//
// It exists because the broadcast carrier is at-most-once and drops rather than
// blocks: a terminal progress event can be lost, and a lost terminal event has
// no successor, so a stream waiting only on the bus would hang until the client
// gave up and the user would read a finished job as one that produced nothing.
// The row is where the answer is written before it is ever broadcast, so the
// stream ends on the TABLE and merely ends FASTER on the broadcast.
const DefaultTerminalRecheck = 15 * time.Second

// SSEBridge provides an HTTP handler that bridges job progress broadcasts to
// SSE: subscribe to the carrier, forward to the SSE client, and close the
// stream when the job is done.
func (d *Dispatcher) SSEHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		jobID := c.Param("id")
		if jobID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "job ID required"})
		}

		// Check flusher support before writing any headers
		flusher, ok := c.Response().Writer.(http.Flusher)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		}

		// Set SSE headers
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)

		// Thread-safe event writer with close guard to prevent writes after handler returns
		var mu sync.Mutex
		var closed atomic.Bool
		sendEvent := func(event string, data any) {
			if closed.Load() {
				return
			}
			jsonData, err := json.Marshal(data)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprintf(c.Response(), "event: %s\ndata: %s\n\n", event, jsonData)
			flusher.Flush()
		}

		// Send current job state first
		if status, terminal, ok := d.jobStatus(jobID); ok {
			sendEvent("status", ProgressEvent{JobID: jobID, Status: status})
			// Already finished, so there is nothing to subscribe to.
			if terminal {
				sendEvent("done", ProgressEvent{JobID: jobID, Status: status})
				return nil
			}
		}

		// done is closed when a terminal state event is received, so the
		// handler can return promptly instead of waiting for client disconnect.
		done := make(chan struct{})
		closeOnce := sync.Once{}
		finish := func(evt ProgressEvent) {
			sendEvent("done", evt)
			closeOnce.Do(func() { close(done) })
		}

		// Subscribe to progress events for this job
		sub, err := d.SubscribeProgress(jobID, func(evt ProgressEvent) {
			sendEvent("progress", evt)

			// Close the stream on terminal states
			if IsTerminalJobStatus(evt.Status) {
				finish(evt)
			}
		})
		if err != nil {
			// Headers already written as SSE — cannot send JSON error; use SSE event instead
			sendEvent("error", map[string]string{"error": "failed to subscribe"})
			return nil
		}
		// Deferred, not called on the way out. Every return below this line
		// leaks a handler otherwise, and the leak has no symptom: on the
		// PostgreSQL carrier only the first subscriber of a channel issues a
		// LISTEN and the rest are in-process filters, so a replica that has
		// served ten thousand of these streams simply runs ten thousand
		// closures per notification and reports nothing.
		defer func() {
			closed.Store(true)
			if uerr := sub.Unsubscribe(); uerr != nil {
				xlog.Warn("Failed to close a job progress subscription", "job_id", jobID, "error", uerr)
			}
		}()

		// The row, once, immediately after subscribing. A job that finished
		// between the read above and this subscription published its terminal
		// event into the window where nobody was listening, and this carrier
		// has no replay, so without this read the stream waits for an event
		// that has already been and gone.
		if status, terminal, ok := d.jobStatus(jobID); ok && terminal {
			finish(ProgressEvent{JobID: jobID, Status: status})
			return nil
		}

		// And the row again, periodically, for the rest of the stream's life.
		// The carrier drops a broadcast rather than blocking when a subscriber
		// falls behind, and a dropped TERMINAL event is a stream that never
		// ends. Reading the row is what keeps a lost broadcast from reading as
		// a job that produced no result.
		ticker := time.NewTicker(d.terminalRecheckInterval())
		defer ticker.Stop()

		for {
			select {
			case <-c.Request().Context().Done():
				return nil
			case <-done:
				return nil
			case <-ticker.C:
				if status, terminal, ok := d.jobStatus(jobID); ok && terminal {
					finish(ProgressEvent{JobID: jobID, Status: status})
					return nil
				}
			}
		}
	}
}

// jobStatus reads a job's status from the TABLE and reports whether it is
// terminal. The third return separates "the row says pending" from "there is no
// row to read", which the caller must not collapse: a job whose row cannot be
// read has not been shown to have produced nothing.
func (d *Dispatcher) jobStatus(jobID string) (status string, terminal, ok bool) {
	if d.store == nil {
		return "", false, false
	}
	job, err := d.store.GetJob(jobID)
	if err != nil {
		return "", false, false
	}
	return job.Status, IsTerminalJobStatus(job.Status), true
}

// terminalRecheckInterval is how often an open stream re-reads the row.
// Configurable so a spec can drive the recovery without waiting out the
// production interval, and never zero, which would spin.
func (d *Dispatcher) terminalRecheckInterval() time.Duration {
	if d.terminalRecheck > 0 {
		return d.terminalRecheck
	}
	return DefaultTerminalRecheck
}
