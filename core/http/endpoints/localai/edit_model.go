package localai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	httpUtils "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
)

// GetEditModelPage renders the edit model page with current configuration
func GetEditModelPage(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	svc := modeladmin.NewConfigService(cl, appConfig)
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		view, err := svc.GetConfig(c.Request().Context(), modelName)
		if err != nil {
			return c.JSON(httpStatusForModelAdminError(err), ModelResponse{Success: false, Error: err.Error()})
		}
		// Render the edit page with the current configuration. Re-fetch the
		// in-memory config from the loader for the template — the on-disk YAML
		// view from svc doesn't carry the loader's parsed struct fields.
		modelConfig, _ := cl.GetModelConfig(modelName)
		templateData := struct {
			Title                  string
			ModelName              string
			Config                 *config.ModelConfig
			ConfigJSON             string
			ConfigYAML             string
			BaseURL                string
			Version                string
			DisableRuntimeSettings bool
		}{
			Title:                  "LocalAI - Edit Model " + modelName,
			ModelName:              modelName,
			Config:                 &modelConfig,
			ConfigYAML:             view.YAML,
			BaseURL:                httpUtils.BaseURL(c),
			Version:                internal.PrintableVersion(),
			DisableRuntimeSettings: appConfig.DisableRuntimeSettings,
		}

		return c.Render(http.StatusOK, "views/model-editor", templateData)
	}
}

// EditModelEndpoint handles updating existing model configurations
func EditModelEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	svc := modeladmin.NewConfigService(cl, appConfig)
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: "Failed to read request body: " + err.Error()})
		}
		result, err := svc.EditYAML(c.Request().Context(), modelName, body, ml)
		if err != nil {
			return c.JSON(httpStatusForModelAdminError(err), ModelResponse{Success: false, Error: err.Error()})
		}
		msg := fmt.Sprintf("Model '%s' updated successfully. Model has been reloaded with new configuration.", result.NewName)
		if result.Renamed {
			msg = fmt.Sprintf("Model '%s' renamed to '%s' and updated successfully.", result.OldName, result.NewName)
		}
		return c.JSON(http.StatusOK, ModelResponse{
			Success:  true,
			Message:  msg,
			Filename: result.Filename,
			Config:   result.Config,
		})
	}
}

// httpStatusForModelAdminError maps the typed errors from modeladmin to
// the HTTP status codes the existing endpoints used to return — keeps the
// REST contract identical after the refactor.
func httpStatusForModelAdminError(err error) int {
	switch {
	case errors.Is(err, modeladmin.ErrNameRequired),
		errors.Is(err, modeladmin.ErrEmptyBody),
		errors.Is(err, modeladmin.ErrPathSeparator),
		errors.Is(err, modeladmin.ErrBadAction),
		errors.Is(err, modeladmin.ErrInvalidConfig):
		return http.StatusBadRequest
	case errors.Is(err, modeladmin.ErrNotFound),
		errors.Is(err, modeladmin.ErrConfigFileMissing):
		return http.StatusNotFound
	case errors.Is(err, modeladmin.ErrPathNotTrusted):
		return http.StatusForbidden
	case errors.Is(err, modeladmin.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// ReloadModelsEndpoint handles reloading model configurations from disk
func ReloadModelsEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Reload configurations
		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath, appConfig.ToConfigLoaderOptions()...); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Preload the models
		if err := cl.Preload(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to preload models: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Return success response
		response := ModelResponse{
			Success: true,
			Message: "Model configurations reloaded successfully",
		}
		return c.JSON(http.StatusOK, response)
	}
}
