package localai

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/modeladmin"
)

// TogglePinnedModelEndpoint handles pinning or unpinning a model.
// Pinned models are excluded from idle unloading, LRU eviction, and memory-pressure eviction.
//
// @Summary      Toggle model pinned status
// @Description  Pin or unpin a model. Pinned models stay loaded and are excluded from automatic eviction.
// @Tags         config
// @Param        name    path  string  true  "Model name"
// @Param        action  path  string  true  "Action: 'pin' or 'unpin'"
// @Success      200  {object}  ModelResponse
// @Failure      400  {object}  ModelResponse
// @Failure      404  {object}  ModelResponse
// @Failure      500  {object}  ModelResponse
// @Router       /api/models/toggle-pinned/{name}/{action} [put]
func TogglePinnedModelEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, syncPinnedFn func()) echo.HandlerFunc {
	svc := modeladmin.NewConfigService(cl, appConfig)
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		action := modeladmin.Action(c.Param("action"))
		result, err := svc.TogglePinned(c.Request().Context(), modelName, action, syncPinnedFn)
		if err != nil {
			return c.JSON(httpStatusForModelAdminError(err), ModelResponse{Success: false, Error: err.Error()})
		}
		msg := fmt.Sprintf("Model '%s' has been %sned successfully.", modelName, action)
		if action == modeladmin.ActionPin {
			msg += " The model will be excluded from automatic eviction."
		}
		return c.JSON(http.StatusOK, ModelResponse{Success: true, Message: msg, Filename: result.Filename})
	}
}
