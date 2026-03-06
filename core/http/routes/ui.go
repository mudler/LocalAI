package routes

import (
	"cmp"
	"slices"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	// SPA routes are handled by the 404 fallback in app.go which serves
	// index.html for any unmatched HTML request, enabling client-side routing.

	// Pipeline models API (for the Talk page WebRTC interface)
	app.GET("/api/pipeline-models", func(c echo.Context) error {
		type pipelineModelInfo struct {
			Name          string `json:"name"`
			VAD           string `json:"vad"`
			Transcription string `json:"transcription"`
			LLM           string `json:"llm"`
			TTS           string `json:"tts"`
			Voice         string `json:"voice"`
		}

		pipelineModels := cl.GetModelConfigsByFilter(func(_ string, cfg *config.ModelConfig) bool {
			p := cfg.Pipeline
			return p.VAD != "" && p.Transcription != "" && p.LLM != "" && p.TTS != ""
		})

		slices.SortFunc(pipelineModels, func(a, b config.ModelConfig) int {
			return cmp.Compare(a.Name, b.Name)
		})

		var models []pipelineModelInfo
		for _, cfg := range pipelineModels {
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

	app.GET("/api/traces", func(c echo.Context) error {
		return c.JSON(200, middleware.GetTraces())
	})

	app.POST("/api/traces/clear", func(c echo.Context) error {
		middleware.ClearTraces()
		return c.NoContent(204)
	})

	app.GET("/api/backend-traces", func(c echo.Context) error {
		return c.JSON(200, trace.GetBackendTraces())
	})

	app.POST("/api/backend-traces/clear", func(c echo.Context) error {
		trace.ClearBackendTraces()
		return c.NoContent(204)
	})

}
