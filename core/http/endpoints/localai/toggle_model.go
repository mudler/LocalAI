package localai

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/pkg/model"
)

// ToggleModelEndpoint handles enabling or disabling a model from being loaded on demand.
// When disabled, the model remains in the collection but will not be loaded when requested.
//
// @Summary      Toggle model enabled/disabled status
// @Description  Enable or disable a model from being loaded on demand. Disabled models remain installed but cannot be loaded.
// @Tags         config
// @Param        name  path  string  true  "Model name"
// @Param        action  path  string  true  "Action: 'enable' or 'disable'"
// @Success      200  {object}  ModelResponse
// @Failure      400  {object}  ModelResponse
// @Failure      404  {object}  ModelResponse
// @Failure      500  {object}  ModelResponse
// @Router       /api/models/{name}/{action} [put]
func ToggleStateModelEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	svc := modeladmin.NewConfigService(cl, appConfig)
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		action := modeladmin.Action(c.Param("action"))
		result, err := svc.ToggleState(c.Request().Context(), modelName, action, ml)
		if err != nil {
			return c.JSON(httpStatusForModelAdminError(err), ModelResponse{Success: false, Error: err.Error()})
		}
		msg := fmt.Sprintf("Model '%s' has been %sd successfully.", modelName, action)
		if action == modeladmin.ActionDisable {
			msg += " The model will not be loaded on demand until re-enabled."
		}
		return c.JSON(http.StatusOK, ModelResponse{Success: true, Message: msg, Filename: result.Filename})
	}
}
