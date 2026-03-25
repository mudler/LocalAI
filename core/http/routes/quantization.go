package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services"
)

// RegisterQuantizationRoutes registers quantization API routes.
func RegisterQuantizationRoutes(e *echo.Echo, qService *services.QuantizationService, appConfig *config.ApplicationConfig, quantizationMw echo.MiddlewareFunc) {
	if qService == nil {
		return
	}

	// Service readiness middleware
	readyMw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if qService == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"error": "quantization service is not available",
				})
			}
			return next(c)
		}
	}

	q := e.Group("/api/quantization", readyMw, quantizationMw)
	q.GET("/backends", localai.ListQuantizationBackendsEndpoint(appConfig))
	q.POST("/jobs", localai.StartQuantizationJobEndpoint(qService))
	q.GET("/jobs", localai.ListQuantizationJobsEndpoint(qService))
	q.GET("/jobs/:id", localai.GetQuantizationJobEndpoint(qService))
	q.POST("/jobs/:id/stop", localai.StopQuantizationJobEndpoint(qService))
	q.DELETE("/jobs/:id", localai.DeleteQuantizationJobEndpoint(qService))
	q.GET("/jobs/:id/progress", localai.QuantizationProgressEndpoint(qService))
	q.POST("/jobs/:id/import", localai.ImportQuantizedModelEndpoint(qService))
	q.GET("/jobs/:id/download", localai.DownloadQuantizedModelEndpoint(qService))
}
