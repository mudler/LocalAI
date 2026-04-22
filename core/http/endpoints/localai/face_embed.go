package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// FaceEmbedEndpoint extracts a face embedding vector from an image.
//
// Distinct from /v1/embeddings, which is OpenAI-compatible and text-only
// by contract (its `input` field is a string or string list of TEXT to
// embed). Passing an image data-URI to /v1/embeddings does not work —
// use this endpoint instead.
//
// @Summary Extract a face embedding from an image.
// @Tags face-recognition
// @Param request body schema.FaceEmbedRequest true "query params"
// @Success 200 {object} schema.FaceEmbedResponse "Response"
// @Router /v1/face/embed [post]
func FaceEmbedEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.FaceEmbedRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		img, err := decodeImageInput(input.Img)
		if err != nil {
			return err
		}

		xlog.Debug("FaceEmbed", "model", cfg.Name, "backend", cfg.Backend)
		vec, err := backend.FaceEmbed(img, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}
		return c.JSON(http.StatusOK, schema.FaceEmbedResponse{
			Embedding: vec,
			Dim:       len(vec),
			Model:     cfg.Name,
		})
	}
}
