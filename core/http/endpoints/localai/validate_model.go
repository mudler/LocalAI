package localai

import (
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"gopkg.in/yaml.v3"
)

// ValidateModelEndpoint handles validating model configurations without saving them
func ValidateModelEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Get the raw body
		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to read request body: " + err.Error(),
			}
			return c.JSON(http.StatusBadRequest, response)
		}
		if len(body) == 0 {
			response := ModelResponse{
				Success: false,
				Error:   "Request body is empty",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Check content type to determine how to parse
		contentType := c.Request().Header.Get("Content-Type")
		var modelConfig config.ModelConfig

		// Parse YAML (default) or auto-detect
		if strings.Contains(contentType, "application/x-yaml") || strings.Contains(contentType, "text/yaml") {
			if err := yaml.Unmarshal(body, &modelConfig); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse YAML: " + err.Error(),
				}
				return c.JSON(http.StatusBadRequest, response)
			}
		} else {
			// Auto-detect - assume YAML for model editor
			if err := yaml.Unmarshal(body, &modelConfig); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse YAML: " + err.Error(),
				}
				return c.JSON(http.StatusBadRequest, response)
			}
		}

		// Validate required fields
		if modelConfig.Name == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name is required",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Set defaults before validation
		modelConfig.SetDefaults(appConfig.ToConfigLoaderOptions()...)

		// Validate the configuration
		valid, err := modelConfig.Validate()
		if !valid {
			errorMsg := "Validation failed"
			if err != nil {
				errorMsg = err.Error()
			}
			response := ModelResponse{
				Success: false,
				Error:   errorMsg,
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Validation successful
		response := ModelResponse{
			Success: true,
			Message: "Configuration is valid",
		}
		return c.JSON(http.StatusOK, response)
	}
}
