package localai

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// BackendMonitorEndpoint returns the status of the specified backend
// @Summary Backend monitor endpoint
// @Tags monitoring
// @Param model query string true "Name of the model to monitor"
// @Success 200 {object} proto.StatusResponse "Response"
// @Router /backend/monitor [get]
func BackendMonitorEndpoint(bm *monitoring.BackendMonitorService) echo.HandlerFunc {
	return func(c echo.Context) error {
		model := c.QueryParam("model")
		// Fall back to binding the request body so pre-existing clients that
		// sent `{"model": "..."}` with GET keep working.
		if model == "" {
			input := new(schema.BackendMonitorRequest)
			if err := c.Bind(input); err != nil {
				return err
			}
			model = input.Model
		}
		if model == "" {
			return echo.NewHTTPError(400, "model query parameter is required")
		}

		resp, err := bm.CheckAndSample(model)
		if err != nil {
			return err
		}
		return c.JSON(200, resp)
	}
}

// BackendShutdownEndpoint shuts down the specified backend
// @Summary Backend shutdown endpoint
// @Tags monitoring
// @Param request body schema.BackendMonitorRequest true "Backend statistics request"
// @Router /backend/shutdown [post]
func BackendShutdownEndpoint(bm *monitoring.BackendMonitorService) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.BackendMonitorRequest)
		// Get input data from the request body
		if err := c.Bind(input); err != nil {
			return err
		}
		if input.Model == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "model is required")
		}

		if err := bm.ShutdownModel(input.Model); err != nil {
			// "Not loaded" is a client-side condition, not a server fault, so
			// it should not surface as a 500. In distributed mode this branch
			// used to be reached for models that were running on a worker —
			// only this replica's local store was empty. The loader now
			// consults the node registry first, so reaching it means the
			// model is loaded neither here nor anywhere in the cluster.
			if errors.Is(err, model.ErrModelNotFound) {
				xlog.Info("Shutdown requested for a model that is not loaded", "model", input.Model)
				return echo.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("model %q is not loaded on this instance or on any worker node", input.Model))
			}
			xlog.Error("Failed to shut down model", "model", input.Model, "error", err)
			return err
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "model stopped"})
	}
}
