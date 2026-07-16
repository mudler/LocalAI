package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// LoadModelEndpoint pre-loads a model into memory by name — the inverse of
// /backend/shutdown. For a realtime pipeline model every configured sub-model
// (VAD, transcription, LLM, TTS, sound_detection, voice_recognition) is loaded; for a regular
// model its own backend is loaded. The call blocks until loading finishes so
// clients can drive warm-up explicitly and learn up front whether a model
// fails to load.
// @Summary Pre-load a model into memory
// @Description Loads the named model (or, for a realtime pipeline, all of its sub-models) into memory so subsequent requests pay no cold-start cost. The inverse of /backend/shutdown.
// @Tags monitoring
// @Accept json
// @Produce json
// @Param request body schema.ModelLoadRequest true "Model to load"
// @Success 200 {object} schema.ModelLoadResponse "Model loaded"
// @Failure 400 {object} schema.ModelLoadResponse "Missing model name"
// @Failure 500 {object} schema.ModelLoadResponse "Load failed (Loaded lists any sub-models that did load)"
// @Router /backend/load [post]
func LoadModelEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.ModelLoadRequest)
		if err := c.Bind(input); err != nil {
			return err
		}
		if input.Model == "" {
			return c.JSON(http.StatusBadRequest, schema.ModelLoadResponse{Message: "model is required"})
		}

		loaded, err := backend.PreloadModelByName(c.Request().Context(), cl, ml, appConfig, input.Model)
		if err != nil {
			xlog.Error("failed to pre-load model", "model", input.Model, "loaded", loaded, "error", err)
			return c.JSON(http.StatusInternalServerError, schema.ModelLoadResponse{
				Loaded:  loaded,
				Message: "failed to load model: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, schema.ModelLoadResponse{
			Loaded:  loaded,
			Message: "model loaded",
		})
	}
}
