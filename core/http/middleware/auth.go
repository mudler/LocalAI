package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
)

var ErrMissingOrMalformedAPIKey = errors.New("missing or malformed API Key")

// GetKeyAuthConfig returns Echo's KeyAuth middleware configuration
func GetKeyAuthConfig(applicationConfig *config.ApplicationConfig) (echo.MiddlewareFunc, error) {
	// Create validator function
	validator := getApiKeyValidationFunction(applicationConfig)

	// Create error handler
	errorHandler := getApiKeyErrorHandler(applicationConfig)

	// Create Next function (skip middleware for certain requests)
	skipper := getApiKeyRequiredFilterFunction(applicationConfig)

	// Wrap it with our custom key lookup that checks multiple sources
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if len(applicationConfig.ApiKeys) == 0 {
				return next(c)
			}

			// Skip if skipper says so
			if skipper != nil && skipper(c) {
				return next(c)
			}

			// Try to extract key from multiple sources
			key, err := extractKeyFromMultipleSources(c)
			if err != nil {
				return errorHandler(err, c)
			}

			// Validate the key
			valid, err := validator(key, c)
			if err != nil || !valid {
				return errorHandler(ErrMissingOrMalformedAPIKey, c)
			}

			// Store key in context for later use
			c.Set("api_key", key)

			return next(c)
		}
	}, nil
}

// extractKeyFromMultipleSources checks multiple sources for the API key
// in order: Authorization header, x-api-key header, xi-api-key header, token cookie
func extractKeyFromMultipleSources(c echo.Context) (string, error) {
	// Check Authorization header first
	auth := c.Request().Header.Get("Authorization")
	if auth != "" {
		// Check for Bearer scheme
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer "), nil
		}
		// If no Bearer prefix, return as-is (for backward compatibility)
		return auth, nil
	}

	// Check x-api-key header
	if key := c.Request().Header.Get("x-api-key"); key != "" {
		return key, nil
	}

	// Check xi-api-key header
	if key := c.Request().Header.Get("xi-api-key"); key != "" {
		return key, nil
	}

	// Check token cookie
	cookie, err := c.Cookie("token")
	if err == nil && cookie != nil && cookie.Value != "" {
		return cookie.Value, nil
	}

	return "", ErrMissingOrMalformedAPIKey
}

func getApiKeyErrorHandler(applicationConfig *config.ApplicationConfig) func(error, echo.Context) error {
	return func(err error, c echo.Context) error {
		if errors.Is(err, ErrMissingOrMalformedAPIKey) {
			if len(applicationConfig.ApiKeys) == 0 {
				return nil // if no keys are set up, any error we get here is not an error.
			}
			c.Response().Header().Set("WWW-Authenticate", "Bearer")
			if applicationConfig.OpaqueErrors {
				return c.NoContent(http.StatusUnauthorized)
			}

			// Check if the request content type is JSON
			contentType := c.Request().Header.Get("Content-Type")
			if strings.Contains(contentType, "application/json") {
				return c.JSON(http.StatusUnauthorized, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "An authentication key is required",
						Code:    401,
						Type:    "invalid_request_error",
					},
				})
			}

			return c.Render(http.StatusUnauthorized, "views/login", map[string]interface{}{
				"BaseURL": BaseURL(c),
			})
		}
		if applicationConfig.OpaqueErrors {
			return c.NoContent(http.StatusInternalServerError)
		}
		return err
	}
}

func getApiKeyValidationFunction(applicationConfig *config.ApplicationConfig) func(string, echo.Context) (bool, error) {
	if applicationConfig.UseSubtleKeyComparison {
		return func(key string, c echo.Context) (bool, error) {
			if len(applicationConfig.ApiKeys) == 0 {
				return true, nil // If no keys are setup, accept everything
			}
			for _, validKey := range applicationConfig.ApiKeys {
				if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) == 1 {
					return true, nil
				}
			}
			return false, ErrMissingOrMalformedAPIKey
		}
	}

	return func(key string, c echo.Context) (bool, error) {
		if len(applicationConfig.ApiKeys) == 0 {
			return true, nil // If no keys are setup, accept everything
		}
		for _, validKey := range applicationConfig.ApiKeys {
			if key == validKey {
				return true, nil
			}
		}
		return false, ErrMissingOrMalformedAPIKey
	}
}

func getApiKeyRequiredFilterFunction(applicationConfig *config.ApplicationConfig) middleware.Skipper {
	return func(c echo.Context) bool {
		path := c.Request().URL.Path

		for _, p := range applicationConfig.PathWithoutAuth {
			if strings.HasPrefix(path, p) {
				return true
			}
		}

		// Handle GET request exemptions if enabled
		if applicationConfig.DisableApiKeyRequirementForHttpGet {
			if c.Request().Method != http.MethodGet {
				return false
			}
			for _, rx := range applicationConfig.HttpGetExemptedEndpoints {
				if rx.MatchString(c.Path()) {
					return true
				}
			}
		}

		return false
	}
}
