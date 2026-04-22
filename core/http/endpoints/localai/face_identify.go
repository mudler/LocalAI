package localai

import (
	"cmp"
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

// defaultIdentifyThreshold is the cosine-distance cutoff applied when
// the client does not specify one. Tuned for buffalo_l ArcFace R50;
// other recognizers (e.g. SFace) should override it explicitly.
const defaultIdentifyThreshold = float32(0.35)

// FaceIdentifyEndpoint runs 1:N identification against the registered store.
// @Summary Identify a face against the registered database (1:N recognition).
// @Tags face-recognition
// @Param request body schema.FaceIdentifyRequest true "query params"
// @Success 200 {object} schema.FaceIdentifyResponse "Response"
// @Router /v1/face/identify [post]
func FaceIdentifyEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, registry facerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.FaceIdentifyRequest)
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

		topK := cmp.Or(input.TopK, 5)
		threshold := cmp.Or(input.Threshold, defaultIdentifyThreshold)

		xlog.Debug("FaceIdentify", "model", cfg.Name, "topK", topK, "threshold", threshold)
		probe, err := backend.FaceEmbed(img, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		matches, err := registry.Identify(c.Request().Context(), probe, topK)
		if err != nil {
			return err
		}

		response := schema.FaceIdentifyResponse{
			Matches: make([]schema.FaceIdentifyMatch, len(matches)),
		}
		for i, m := range matches {
			confidence := (1 - m.Distance/threshold) * 100
			if confidence < 0 {
				confidence = 0
			}
			if confidence > 100 {
				confidence = 100
			}
			response.Matches[i] = schema.FaceIdentifyMatch{
				ID:         m.ID,
				Name:       m.Metadata.Name,
				Labels:     m.Metadata.Labels,
				Distance:   m.Distance,
				Confidence: confidence,
				Match:      m.Distance <= threshold,
			}
		}
		return c.JSON(http.StatusOK, response)
	}
}
