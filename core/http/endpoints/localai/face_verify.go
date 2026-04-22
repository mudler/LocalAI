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

// FaceVerifyEndpoint compares two images and reports whether they depict the same person.
// @Summary Verify that two images depict the same person.
// @Tags face-recognition
// @Param request body schema.FaceVerifyRequest true "query params"
// @Success 200 {object} schema.FaceVerifyResponse "Response"
// @Router /v1/face/verify [post]
func FaceVerifyEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.FaceVerifyRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		img1, err := decodeImageInput(input.Img1)
		if err != nil {
			return err
		}
		img2, err := decodeImageInput(input.Img2)
		if err != nil {
			return err
		}

		xlog.Debug("FaceVerify", "model", cfg.Name, "backend", cfg.Backend)
		res, err := backend.FaceVerify(img1, img2, input.Threshold, input.AntiSpoofing, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		return c.JSON(http.StatusOK, schema.FaceVerifyResponse{
			Verified:   res.GetVerified(),
			Distance:   res.GetDistance(),
			Threshold:  res.GetThreshold(),
			Confidence: res.GetConfidence(),
			Model:      res.GetModel(),
			Img1Area: schema.FacialArea{
				X: res.GetImg1Area().GetX(),
				Y: res.GetImg1Area().GetY(),
				W: res.GetImg1Area().GetW(),
				H: res.GetImg1Area().GetH(),
			},
			Img2Area: schema.FacialArea{
				X: res.GetImg2Area().GetX(),
				Y: res.GetImg2Area().GetY(),
				W: res.GetImg2Area().GetW(),
				H: res.GetImg2Area().GetH(),
			},
			ProcessingTimeMs: res.GetProcessingTimeMs(),
		})
	}
}
