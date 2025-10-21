package middleware

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/services"
	"github.com/rs/zerolog/log"
)

// MetricsMiddleware creates a middleware that tracks API usage metrics
// Note: Uses CONTEXT_LOCALS_KEY_MODEL_NAME constant defined in request.go
func MetricsMiddleware(metricsStore services.MetricsStore) fiber.Handler {
	return func(c *fiber.Ctx) error {
		path := c.Path()

		// Skip tracking for UI routes, static files, and non-API endpoints
		if shouldSkipMetrics(path) {
			return c.Next()
		}

		// Record start time
		start := time.Now()

		// Get endpoint category
		endpoint := categorizeEndpoint(path)

		// Continue with the request
		err := c.Next()

		// Record metrics after request completes
		duration := time.Since(start)
		success := err == nil && c.Response().StatusCode() < 400

		// Extract model name from context (set by RequestExtractor middleware)
		// Use the same constant as RequestExtractor
		model := "unknown"
		if modelVal, ok := c.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string); ok && modelVal != "" {
			model = modelVal
			log.Debug().Str("model", model).Str("endpoint", endpoint).Msg("Recording metrics for request")
		} else {
			// Fallback: try to extract from path params or query
			model = extractModelFromRequest(c)
			log.Debug().Str("model", model).Str("endpoint", endpoint).Msg("Recording metrics for request (fallback)")
		}

		// Extract backend from response headers if available
		backend := string(c.Response().Header.Peek("X-LocalAI-Backend"))

		// Record the request
		metricsStore.RecordRequest(endpoint, model, backend, success, duration)

		return err
	}
}

// shouldSkipMetrics determines if a request should be excluded from metrics
func shouldSkipMetrics(path string) bool {
	// Skip UI routes
	skipPrefixes := []string{
		"/views/",
		"/static/",
		"/browse/",
		"/chat/",
		"/text2image/",
		"/tts/",
		"/talk/",
		"/models/edit/",
		"/import-model",
		"/settings",
		"/api/models",     // UI API endpoints
		"/api/backends",   // UI API endpoints
		"/api/operations", // UI API endpoints
		"/api/p2p",        // UI API endpoints
		"/api/metrics",    // Metrics API itself
	}

	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	// Also skip root path and other UI pages
	if path == "/" || path == "/index" {
		return true
	}

	return false
}

// categorizeEndpoint maps request paths to friendly endpoint categories
func categorizeEndpoint(path string) string {
	// OpenAI-compatible endpoints
	if strings.HasPrefix(path, "/v1/chat/completions") || strings.HasPrefix(path, "/chat/completions") {
		return "chat"
	}
	if strings.HasPrefix(path, "/v1/completions") || strings.HasPrefix(path, "/completions") {
		return "completions"
	}
	if strings.HasPrefix(path, "/v1/embeddings") || strings.HasPrefix(path, "/embeddings") {
		return "embeddings"
	}
	if strings.HasPrefix(path, "/v1/images/generations") || strings.HasPrefix(path, "/images/generations") {
		return "image-generation"
	}
	if strings.HasPrefix(path, "/v1/audio/transcriptions") || strings.HasPrefix(path, "/audio/transcriptions") {
		return "transcriptions"
	}
	if strings.HasPrefix(path, "/v1/audio/speech") || strings.HasPrefix(path, "/audio/speech") {
		return "text-to-speech"
	}
	if strings.HasPrefix(path, "/v1/models") || strings.HasPrefix(path, "/models") {
		return "models"
	}

	// LocalAI-specific endpoints
	if strings.HasPrefix(path, "/v1/internal") {
		return "internal"
	}
	if strings.Contains(path, "/tts") {
		return "text-to-speech"
	}
	if strings.Contains(path, "/stt") || strings.Contains(path, "/whisper") {
		return "speech-to-text"
	}
	if strings.Contains(path, "/sound-generation") {
		return "sound-generation"
	}

	// Default to the first path segment
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}

	return "unknown"
}

// extractModelFromRequest attempts to extract the model name from the request
func extractModelFromRequest(c *fiber.Ctx) string {
	// Try query parameter first
	model := c.Query("model")
	if model != "" {
		return model
	}

	// Try to extract from JSON body for POST requests
	if c.Method() == fiber.MethodPost {
		// Read body
		bodyBytes := c.Body()
		if len(bodyBytes) > 0 {
			// Parse JSON
			var reqBody map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &reqBody); err == nil {
				if modelVal, ok := reqBody["model"]; ok {
					if modelStr, ok := modelVal.(string); ok {
						return modelStr
					}
				}
			}
		}
	}

	// Try path parameter for endpoints like /models/:model
	model = c.Params("model")
	if model != "" {
		return model
	}

	return "unknown"
}
