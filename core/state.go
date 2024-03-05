package core

import (
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/model"
)

// TODO: Can I come up with a better name or location for this?
// Is this even a good idea? Test that first!!!!
// The purpose of this structure is to hold pointers to all initialized services, to make plumbing easy
type Application struct {
	// Application-Level Config
	// TODO: Should this eventually be broken up further?
	ApplicationConfig *config.ApplicationConfig

	// Core Low-Level Services
	BackendConfigLoader *config.BackendConfigLoader
	ModelLoader         *model.ModelLoader

	// Built-In High Level Services
	BackendMonitor        *services.BackendMonitor
	GalleryService        *services.GalleryService
	LocalAIMetricsService *services.LocalAIMetricsService

	// Backend Calling Services
	TranscriptionBackendService *backend.TranscriptionBackendService
}

// TODO: Break up ApplicationConfig.
// Migrate over stuff that is not set via config at all - especially runtime stuff
type ApplicationState struct {
}
