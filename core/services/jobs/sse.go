package jobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
)

// SSEBridge provides an HTTP handler that bridges NATS progress events to SSE.
// This follows the notetaker pattern: subscribe to NATS, forward to SSE client.
func (d *Dispatcher) SSEHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		jobID := c.Param("id")
		if jobID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "job ID required"})
		}

		// Set SSE headers
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)

		flusher, ok := c.Response().Writer.(http.Flusher)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		}

		// Thread-safe event writer
		var mu sync.Mutex
		sendEvent := func(event string, data any) {
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
		job, err := d.store.GetJob(jobID)
		if err == nil {
			sendEvent("status", ProgressEvent{
				JobID:  jobID,
				Status: job.Status,
			})
		}

		// Subscribe to progress events for this job
		sub, err := d.SubscribeProgress(jobID, func(evt ProgressEvent) {
			sendEvent("progress", evt)

			// Close the stream on terminal states
			if evt.Status == "completed" || evt.Status == "failed" || evt.Status == "cancelled" {
				sendEvent("done", evt)
			}
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to subscribe"})
		}
		defer sub.Unsubscribe()

		// Wait for client disconnect
		<-c.Request().Context().Done()
		return nil
	}
}
