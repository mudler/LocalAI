package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
)

// BackendMonitorEndpoint returns the status of the specified backend
// @Summary Backend monitor endpoint
// @Param request body schema.BackendMonitorRequest true "Backend statistics request"
// @Success 200 {object} proto.StatusResponse "Response"
// @Router /backend/monitor [get]
func BackendMonitorEndpoint(bm *services.BackendMonitorService) echo.HandlerFunc {
	return func(c echo.Context) error {

		input := new(schema.BackendMonitorRequest)
		// Get input data from the request body
		if err := c.Bind(input); err != nil {
			return err
		}

		resp, err := bm.CheckAndSample(input.Model)
		if err != nil {
			return err
		}
		return c.JSON(200, resp)
	}
}

// BackendShutdownEndpoint shuts down the specified backend
// @Summary Backend monitor endpoint
// @Param request body schema.BackendMonitorRequest true "Backend statistics request"
// @Router /backend/shutdown [post]
func BackendShutdownEndpoint(bm *services.BackendMonitorService) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.BackendMonitorRequest)
		// Get input data from the request body
		if err := c.Bind(input); err != nil {
			return err
		}

		return bm.ShutdownModel(input.Model)
	}
}
