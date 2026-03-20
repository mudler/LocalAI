package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services"
)

// RegisterFineTuningRoutes registers fine-tuning API routes.
func RegisterFineTuningRoutes(e *echo.Echo, ftService *services.FineTuneService, appConfig *config.ApplicationConfig, fineTuningMw echo.MiddlewareFunc) {
	if ftService == nil {
		return
	}

	// Service readiness middleware
	readyMw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if ftService == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"error": "fine-tuning service is not available",
				})
			}
			return next(c)
		}
	}

	ft := e.Group("/api/fine-tuning", readyMw, fineTuningMw)
	ft.GET("/backends", localai.ListFineTuneBackendsEndpoint(appConfig))
	ft.POST("/jobs", localai.StartFineTuneJobEndpoint(ftService))
	ft.GET("/jobs", localai.ListFineTuneJobsEndpoint(ftService))
	ft.GET("/jobs/:id", localai.GetFineTuneJobEndpoint(ftService))
	ft.DELETE("/jobs/:id", localai.StopFineTuneJobEndpoint(ftService))
	ft.GET("/jobs/:id/progress", localai.FineTuneProgressEndpoint(ftService))
	ft.GET("/jobs/:id/checkpoints", localai.ListCheckpointsEndpoint(ftService))
	ft.POST("/jobs/:id/export", localai.ExportModelEndpoint(ftService))
	ft.POST("/datasets", localai.UploadDatasetEndpoint(ftService))
}
