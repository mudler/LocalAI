package localai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// ModelLoadCancelEndpoint persists generation-specific cancellation intent.
// @Summary Cancel one distributed load generation
// @Tags models
// @Accept json
// @Produce json
// @Param id path string true "Model ID"
// @Param request body schema.ModelLoadCancelRequest true "Exact generation"
// @Success 200 {object} schema.ModelLoadCancelResponse "Confirmed completion"
// @Success 202 {object} schema.ModelLoadCancelResponse "Durable pending or uncertain cancellation"
// @Failure 400 {object} schema.ErrorResponse
// @Failure 401 {object} schema.ErrorResponse
// @Failure 403 {object} schema.ErrorResponse
// @Failure 404 {object} schema.ErrorResponse
// @Failure 409 {object} schema.ErrorResponse
// @Router /api/models/{id}/load-cancel [post]
func ModelLoadCancelEndpoint(service func() *nodes.LoadRecoveryService, stopper nodes.LoadOperationStopper) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.ModelLoadCancelRequest
		dec := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return c.JSON(400, nodeError(400, "job_id required in a JSON object"))
		}
		if strings.TrimSpace(req.JobID) == "" || dec.Decode(new(any)) != io.EOF {
			return c.JSON(400, nodeError(400, "invalid cancellation body"))
		}
		var s *nodes.LoadRecoveryService
		if service != nil {
			s = service()
		}
		if s == nil {
			return c.JSON(404, nodeError(404, "unknown load generation"))
		}
		result, err := s.Cancel(c.Request().Context(), nodes.LoadJobRef{TrackingKey: c.Param("id"), Generation: req.JobID}, stopper)
		if err != nil {
			code := 500
			if errors.Is(err, nodes.ErrLoadJobConflict) {
				code = 409
			}
			if errors.Is(err, nodes.ErrLoadJobUnknown) {
				code = 404
			}
			return c.JSON(code, nodeError(code, err.Error()))
		}
		code := http.StatusAccepted
		if result.Outcome == nodes.LoadTerminalConfirmed {
			code = 200
		}
		return c.JSON(code, schema.ModelLoadCancelResponse{Model: c.Param("id"), JobID: req.JobID, State: string(result.Outcome)})
	}
}
