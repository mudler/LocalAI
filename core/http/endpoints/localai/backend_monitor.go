package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/monitoring"
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

		return bm.ShutdownModel(input.Model)
	}
}
