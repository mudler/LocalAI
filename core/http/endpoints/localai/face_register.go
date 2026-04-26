package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/facerecognition"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// FaceRegisterEndpoint enrolls a face into the 1:N identification store.
// @Summary Register a face for 1:N identification.
// @Tags face-recognition
// @Param request body schema.FaceRegisterRequest true "query params"
// @Success 200 {object} schema.FaceRegisterResponse "Response"
// @Router /v1/face/register [post]
func FaceRegisterEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, registry facerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.FaceRegisterRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}
		if input.Name == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "name is required")
		}

		img, err := decodeImageInput(input.Img)
		if err != nil {
			return err
		}

		xlog.Debug("FaceRegister", "model", cfg.Name, "name", input.Name)
		embedding, err := backend.FaceEmbed(img, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		stored, err := registry.Register(c.Request().Context(), embedding, facerecognition.Metadata{
			Name:   input.Name,
			Labels: input.Labels,
		})
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, schema.FaceRegisterResponse{
			ID:           stored.ID,
			Name:         stored.Name,
			RegisteredAt: stored.RegisteredAt,
		})
	}
}
