package routes

import (
	"cmp"
	"slices"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	appConfig *config.ApplicationConfig,
	galleryService *galleryop.GalleryService,
	adminMiddleware echo.MiddlewareFunc) {

	// SPA routes are handled by the 404 fallback in app.go which serves
	// index.html for any unmatched HTML request, enabling client-side routing.

	// Canvas HTML previews: served from their own route so the response can
	// carry a CSP that allows CDN scripts (see localai.canvasPreviewCSP).
	canvasPreviews := localai.NewCanvasPreviewStore()
	app.POST("/api/canvas/preview", localai.CreateCanvasPreviewEndpoint(canvasPreviews))
	app.GET("/api/canvas/preview/:id", localai.GetCanvasPreviewEndpoint(canvasPreviews))

	// Pipeline models API (for the Talk page WebRTC interface).
	// A model qualifies when it either declares an explicit STT+LLM+TTS (the VAD is optional)
	// pipeline (legacy/composed) or is an any-to-any speech-to-speech model
	// (realtime_audio plus pipeline.any_to_any). The second one runs the whole
	// loop in a single AudioToAudioStream RPC, so it needs no slots: its VAD
	// is optional and the other slots carry its own name.
	app.GET("/api/pipeline-models", func(c echo.Context) error {
		type pipelineModelInfo struct {
			Name          string `json:"name"`
			VAD           string `json:"vad"`
			Transcription string `json:"transcription"`
			LLM           string `json:"llm"`
			TTS           string `json:"tts"`
			Voice         string `json:"voice"`
			// AnyToAny is true for a speech-to-speech model. The Talk page shows
			// it as one model, with no four-slot view.
			AnyToAny bool `json:"any_to_any,omitempty"`
			// SelfContained is the older name of AnyToAny, kept for clients
			// built before the field existed.
			SelfContained bool `json:"self_contained,omitempty"`
		}

		pipelineModels := cl.GetModelConfigsByFilter(func(_ string, cfg *config.ModelConfig) bool {
			if cfg.Pipeline.IsAnyToAny() && cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
				return true
			}
			p := cfg.Pipeline
			return p.Transcription != "" && p.LLM != "" && p.TTS != ""
		})

		slices.SortFunc(pipelineModels, func(a, b config.ModelConfig) int {
			return cmp.Compare(a.Name, b.Name)
		})

		models := make([]pipelineModelInfo, 0, len(pipelineModels))
		for _, cfg := range pipelineModels {
			if cfg.Pipeline.IsAnyToAny() && cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
				models = append(models, pipelineModelInfo{
					Name:          cfg.Name,
					VAD:           cfg.Pipeline.VAD,
					Transcription: cfg.Name,
					LLM:           cfg.Name,
					TTS:           cfg.Name,
					Voice:         cfg.TTSConfig.Voice,
					AnyToAny:      true,
					SelfContained: true,
				})
				continue
			}
			models = append(models, pipelineModelInfo{
				Name:          cfg.Name,
				VAD:           cfg.Pipeline.VAD,
				Transcription: cfg.Pipeline.Transcription,
				LLM:           cfg.Pipeline.LLM,
				TTS:           cfg.Pipeline.TTS,
				Voice:         cfg.TTSConfig.Voice,
			})
		}

		return c.JSON(200, models)
	})
}
