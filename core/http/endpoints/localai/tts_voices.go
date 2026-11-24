package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"gorm.io/gorm"
)

// TTSModelVoices groups named voices by installed model.
type TTSModelVoices struct {
	Model  string            `json:"model"`
	Voices []config.TTSVoice `json:"voices"`
}

// TTSVoicesResponse is returned by the TTS voice discovery endpoint.
type TTSVoicesResponse struct {
	Data []TTSModelVoices `json:"data"`
}

// TTSVoicesEndpoint lists named voices advertised by installed model configs.
//
// @Summary List text-to-speech voices
// @Description List named voices and their language and gender metadata. Use the optional model query parameter to filter the response.
// @Tags audio
// @Produce json
// @Param model query string false "Installed model name"
// @Success 200 {object} TTSVoicesResponse
// @Failure 404 {object} schema.ErrorResponse
// @Router /v1/audio/voices [get]
func TTSVoicesEndpoint(loader *config.ModelConfigLoader, databases ...*gorm.DB) echo.HandlerFunc {
	var authDB *gorm.DB
	if len(databases) > 0 {
		authDB = databases[0]
	}
	return func(c echo.Context) error {
		allowed, err := ttsVoiceModelAllowlist(c, authDB)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, schema.ErrorResponse{Error: &schema.APIError{
				Code: http.StatusInternalServerError, Message: "failed to check permissions", Type: "server_error",
			}})
		}
		modelName := c.QueryParam("model")
		if modelName != "" {
			cfg, ok := loader.GetModelConfig(modelName)
			if !ok || (allowed != nil && !allowed[modelName]) {
				return c.JSON(http.StatusNotFound, schema.ErrorResponse{Error: &schema.APIError{
					Code: http.StatusNotFound, Message: "model not found", Type: "not_found",
				}})
			}
			return c.JSON(http.StatusOK, TTSVoicesResponse{Data: []TTSModelVoices{{
				Model: cfg.Name, Voices: ttsVoicesForConfig(loader, &cfg),
			}}})
		}

		response := TTSVoicesResponse{Data: []TTSModelVoices{}}
		for _, cfg := range loader.GetAllModelsConfigs() {
			if allowed != nil && !allowed[cfg.Name] {
				continue
			}
			voices := ttsVoicesForConfig(loader, &cfg)
			if len(voices) == 0 {
				continue
			}
			response.Data = append(response.Data, TTSModelVoices{Model: cfg.Name, Voices: voices})
		}
		return c.JSON(http.StatusOK, response)
	}
}

func ttsVoicesForConfig(loader *config.ModelConfigLoader, cfg *config.ModelConfig) []config.TTSVoice {
	resolved, isAlias, err := loader.ResolveAlias(cfg)
	if err == nil && isAlias {
		return config.TTSVoicesForModel(resolved)
	}
	return config.TTSVoicesForModel(cfg)
}

func ttsVoiceModelAllowlist(c echo.Context, db *gorm.DB) (map[string]bool, error) {
	if db == nil {
		return nil, nil
	}
	user := auth.GetUser(c)
	if user == nil || user.Role == auth.RoleAdmin {
		return nil, nil
	}
	permissions, err := auth.GetCachedUserPermissions(c, db, user.ID)
	if err != nil || !permissions.AllowedModels.Enabled {
		return nil, err
	}
	allowed := make(map[string]bool, len(permissions.AllowedModels.Models))
	for _, model := range permissions.AllowedModels.Models {
		allowed[model] = true
	}
	return allowed, nil
}
