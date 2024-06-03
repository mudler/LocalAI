package middleware

import (
	"time"

	"github.com/go-skynet/LocalAI/core/services"
	"github.com/gofiber/fiber/v2"
)

type metricsMiddlewareConfig struct {
	Filter         func(c *fiber.Ctx) bool
	metricsService *services.LocalAIMetricsService
}

func GetMetrics(metrics *services.LocalAIMetricsService) fiber.Handler {
	cfg := metricsMiddlewareConfig{
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
