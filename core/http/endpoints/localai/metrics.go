package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// LocalAIMetricsEndpoint returns the metrics endpoint for LocalAI
// @Summary Prometheus metrics endpoint
// @Param request body config.Gallery true "Gallery details"
// @Router /metrics [get]
func LocalAIMetricsEndpoint() fiber.Handler {
	return adaptor.HTTPHandler(promhttp.Handler())
}
