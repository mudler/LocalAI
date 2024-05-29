package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func LocalAIMetricsEndpoint() fiber.Handler {
	return adaptor.HTTPHandler(promhttp.Handler())
}
