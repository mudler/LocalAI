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

// FaceAnalyzeEndpoint returns demographic attributes for faces in an image.
// @Summary Analyze demographic attributes (age, gender, ...) of faces.
// @Tags face-recognition
// @Param request body schema.FaceAnalyzeRequest true "query params"
// @Success 200 {object} schema.FaceAnalyzeResponse "Response"
// @Router /v1/face/analyze [post]
func FaceAnalyzeEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.FaceAnalyzeRequest)
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

		xlog.Debug("FaceAnalyze", "model", cfg.Name, "backend", cfg.Backend, "actions", input.Actions)
		res, err := backend.FaceAnalyze(img, input.Actions, input.AntiSpoofing, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		response := schema.FaceAnalyzeResponse{
			Faces: make([]schema.FaceAnalysis, len(res.GetFaces())),
		}
		for i, f := range res.GetFaces() {
			response.Faces[i] = schema.FaceAnalysis{
				Region: schema.FacialArea{
					X: f.GetRegion().GetX(),
					Y: f.GetRegion().GetY(),
					W: f.GetRegion().GetW(),
					H: f.GetRegion().GetH(),
				},
				FaceConfidence:  f.GetFaceConfidence(),
				Age:             f.GetAge(),
				DominantGender:  f.GetDominantGender(),
				Gender:          f.GetGender(),
				DominantEmotion: f.GetDominantEmotion(),
				Emotion:         f.GetEmotion(),
				DominantRace:    f.GetDominantRace(),
				Race:            f.GetRace(),
				IsReal:          f.GetIsReal(),
				AntispoofScore:  f.GetAntispoofScore(),
			}
		}

		return c.JSON(http.StatusOK, response)
	}
}
