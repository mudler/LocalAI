package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// ModelLoadStatusEndpoint reports the progress of a cold load that is
// currently running for a model, so a client that got a 503 while the model
// stages onto a worker can poll rather than blind-retry.
//
// Read-only and observability-shaped: it is not admin-gated (a caller allowed
// to ask for inference on a model may see why it is not answering yet) and it
// is not feature-gated, since a per-capability gate would make the explanation
// for a 503 depend on which modality the model happens to be.
//
// @Summary Report the progress of an in-flight model load.
// @Description Returns the live state of a distributed cold load: job id, phase, node, byte progress, ETA, lease freshness, and for a failed attempt the cause, whether the stop is pending and when the model is released. 404 means no load exists for the model. A database error is 503, never an empty answer. This is the same `loading` object the 503 response carries while a model is still staging.
// @Tags models
// @Produce json
// @Param id path string true "Model ID"
// @Success 200 {object} schema.ModelLoadingStatus "Live load progress"
// @Failure 404 {object} schema.ErrorResponse "No load is running for this model"
// @Failure 503 {object} schema.ErrorResponse "The job table could not be read"
// @Router /api/models/{id}/load-status [get]
func ModelLoadStatusEndpoint(loadJobs func() nodes.LoadJobStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelID := c.Param("id")
		if modelID == "" {
			return c.JSON(http.StatusBadRequest, schema.ErrorResponse{
				Error: &schema.APIError{Message: "model id is required", Code: http.StatusBadRequest, Type: "invalid_request_error"},
			})
		}

		notLoading := schema.ErrorResponse{
			Error: &schema.APIError{
				Message: "no load is running for model " + modelID,
				Code:    http.StatusNotFound,
				Type:    "not_found_error",
			},
		}

		// Cold-load jobs are a distributed-mode concept: a single-host load is
		// synchronous and has no job to report on.
		var store nodes.LoadJobStore
		if loadJobs != nil {
			store = loadJobs()
		}
		if store == nil {
			return c.JSON(http.StatusNotFound, notLoading)
		}

		job, err := store.GetLoadJob(c.Request().Context(), modelID)
		if err != nil {
			// Fail closed: a database error must not read as "no load is
			// running", or a caller would start a second one.
			return c.JSON(http.StatusServiceUnavailable, schema.ErrorResponse{
				Error: &schema.APIError{Message: err.Error(), Code: http.StatusServiceUnavailable, Type: "server_error"},
			})
		}
		if job == nil {
			return c.JSON(http.StatusNotFound, notLoading)
		}
		return c.JSON(http.StatusOK, nodes.LoadingStatus(job))
	}
}
