package localai

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/mudler/LocalAI/core/services"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func LocalAIMetricsEndpoint() fiber.Handler {

	return adaptor.HTTPHandler(promhttp.Handler())
}

type apiMiddlewareConfig struct {
	Filter         func(c *fiber.Ctx) bool
	metricsService *services.LocalAIMetricsService
}

func LocalAIMetricsAPIMiddleware(metrics *services.LocalAIMetricsService) fiber.Handler {
	cfg := apiMiddlewareConfig{
		metricsService: metrics,
		Filter: func(c *fiber.Ctx) bool {
			return c.Path() == "/metrics"
		},
	}

	return func(c *fiber.Ctx) error {
		if cfg.Filter != nil && cfg.Filter(c) {
			return c.Next()
		}
		path := c.Path()
		method := c.Method()

		start := time.Now()
		err := c.Next()
		elapsed := float64(time.Since(start)) / float64(time.Second)
		cfg.metricsService.ObserveAPICall(method, path, elapsed)
		return err
	}
}
