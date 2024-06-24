package core

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
)

// The purpose of this structure is to hold pointers to all initialized services, to make plumbing easy
// Perhaps a proper DI system is worth it in the future, but for now keep things simple.
type Application struct {

	// Application-Level Config
	ApplicationConfig *config.ApplicationConfig
	// ApplicationState *ApplicationState

	// Core Low-Level Services
	BackendConfigLoader *config.BackendConfigLoader
	ModelLoader         *model.ModelLoader

	// Backend Services
	// EmbeddingsBackendService      *backend.EmbeddingsBackendService
	// ImageGenerationBackendService *backend.ImageGenerationBackendService
	// LLMBackendService             *backend.LLMBackendService
	// TranscriptionBackendService *backend.TranscriptionBackendService
	// TextToSpeechBackendService  *backend.TextToSpeechBackendService

	// LocalAI System Services
	BackendMonitorService *services.BackendMonitorService
	GalleryService        *services.GalleryService
	ListModelsService     *services.ListModelsService
	LocalAIMetricsService *services.LocalAIMetricsService
	// OpenAIService         *services.OpenAIService
}

// TODO [NEXT PR?]: Break up ApplicationConfig.
// Migrate over stuff that is not set via config at all - especially runtime stuff
type ApplicationState struct {
}
