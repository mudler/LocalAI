package localai

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// ModelLoadCancelEndpoint cancels one distributed load attempt.
//
// The body names the exact attempt (job_id, from load-status). That precondition
// is what keeps a cancel from hitting a replacement attempt that started after
// the caller looked. The call is idempotent, and repeating it never extends the
// time the model is held.
//
// @Summary Cancel one distributed model load.
// @Description Cancels the load attempt named by `job_id` and stops its remote work. 200 means the attempt is gone or the worker confirmed the stop. 202 means the cancel is recorded and the stop is pending; the model is released after `retry_after` seconds regardless. 409 means a different attempt is current and carries its `current_job_id`. Repeating the call is safe and does not extend the hold.
// @Tags models
// @Accept json
// @Produce json
// @Param id path string true "Model ID"
// @Param request body schema.ModelLoadCancelRequest true "The exact attempt to cancel"
// @Success 200 {object} schema.ModelLoadCancelResponse "Stopped, or no such load any more"
// @Success 202 {object} schema.ModelLoadCancelResponse "Cancel recorded; stop pending"
// @Failure 400 {object} schema.ErrorResponse
// @Failure 401 {object} schema.ErrorResponse
// @Failure 403 {object} schema.ErrorResponse
// @Failure 404 {object} schema.ErrorResponse "Unknown model"
// @Failure 409 {object} schema.ModelLoadCancelResponse "A different attempt is current"
// @Router /api/models/{id}/load-cancel [post]
func ModelLoadCancelEndpoint(service func() *nodes.LoadCancelService, modelKnown func(id string) bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.ModelLoadCancelRequest
		dec := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "job_id required in a JSON object"))
		}
		if strings.TrimSpace(req.JobID) == "" || dec.Decode(new(any)) != io.EOF {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid cancellation body"))
		}

		var svc *nodes.LoadCancelService
		if service != nil {
			svc = service()
		}
		model := c.Param("id")
		if svc == nil {
			// Not distributed: there are no load jobs to cancel.
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "no distributed loads on this server"))
		}

		result, err := svc.Cancel(c.Request().Context(), nodes.LoadJobRef{TrackingKey: model, Generation: req.JobID})
		if errors.Is(err, nodes.ErrLoadCancelConflict) {
			return c.JSON(http.StatusConflict, schema.ModelLoadCancelResponse{Model: model, JobID: req.JobID, State: "conflict", CurrentJobID: result.CurrentJobID})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, err.Error()))
		}
		if result.State == nodes.LoadCancelGone && modelKnown != nil && !modelKnown(model) {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "unknown model "+model))
		}

		body := schema.ModelLoadCancelResponse{Model: model, JobID: req.JobID, State: string(result.State)}
		code := http.StatusOK
		if result.State == nodes.LoadCancelStopping {
			code = http.StatusAccepted
			body.RetryAfter = int(math.Ceil(result.RetryAfter.Seconds()))
			c.Response().Header().Set("Retry-After", strconv.Itoa(body.RetryAfter))
		}
		return c.JSON(code, body)
	}
}
