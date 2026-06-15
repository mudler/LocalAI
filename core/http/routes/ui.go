package routes

import (
	"cmp"
	"slices"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	appConfig *config.ApplicationConfig,
	galleryService *galleryop.GalleryService,
	adminMiddleware echo.MiddlewareFunc) {

	// SPA routes are handled by the 404 fallback in app.go which serves
	// index.html for any unmatched HTML request, enabling client-side routing.

	// Pipeline models API (for the Talk page WebRTC interface).
	// A model qualifies when it either declares an explicit VAD+STT+LLM+TTS
	// pipeline (legacy/composed) or carries the realtime_audio usecase (a
	// self-contained any-to-any audio backend like liquid-audio that owns the
	// full loop in a single AudioToAudioStream RPC).
	app.GET("/api/pipeline-models", func(c echo.Context) error {
		type pipelineModelInfo struct {
			Name          string `json:"name"`
			VAD           string `json:"vad"`
			Transcription string `json:"transcription"`
			LLM           string `json:"llm"`
			TTS           string `json:"tts"`
			Voice         string `json:"voice"`
			// SelfContained is true for any-to-any audio models — the four
			// pipeline slots are populated with the model's own name so the
			// UI can render them, but the Realtime API routes the session
			// directly to the backend's AudioToAudioStream RPC.
			SelfContained bool `json:"self_contained,omitempty"`
		}

		pipelineModels := cl.GetModelConfigsByFilter(func(_ string, cfg *config.ModelConfig) bool {
			if cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
				return true
			}
			p := cfg.Pipeline
			return p.VAD != "" && p.Transcription != "" && p.LLM != "" && p.TTS != ""
		})

		slices.SortFunc(pipelineModels, func(a, b config.ModelConfig) int {
			return cmp.Compare(a.Name, b.Name)
		})

		models := make([]pipelineModelInfo, 0, len(pipelineModels))
		for _, cfg := range pipelineModels {
			if cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
				models = append(models, pipelineModelInfo{
					Name:          cfg.Name,
					VAD:           cfg.Name,
					Transcription: cfg.Name,
					LLM:           cfg.Name,
					TTS:           cfg.Name,
					Voice:         cfg.TTSConfig.Voice,
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
