package application

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

type Application struct {
	backendLoader      *config.ModelConfigLoader
	modelLoader        *model.ModelLoader
	applicationConfig  *config.ApplicationConfig
	startupConfig      *config.ApplicationConfig // Stores original config from env vars (before file loading)
	templatesEvaluator *templates.Evaluator
	galleryService     *services.GalleryService
	agentJobService    *services.AgentJobService
	agentPoolService   *services.AgentPoolService
	watchdogMutex      sync.Mutex
	watchdogStop       chan bool
	p2pMutex           sync.Mutex
	p2pCtx             context.Context
	p2pCancel          context.CancelFunc
	agentJobMutex      sync.Mutex
}

func newApplication(appConfig *config.ApplicationConfig) *Application {
	return &Application{
		backendLoader:      config.NewModelConfigLoader(appConfig.SystemState.Model.ModelsPath),
		modelLoader:        model.NewModelLoader(appConfig.SystemState),
		applicationConfig:  appConfig,
		templatesEvaluator: templates.NewEvaluator(appConfig.SystemState.Model.ModelsPath),
	}
}

func (a *Application) ModelConfigLoader() *config.ModelConfigLoader {
	return a.backendLoader
}

func (a *Application) ModelLoader() *model.ModelLoader {
	return a.modelLoader
}

func (a *Application) ApplicationConfig() *config.ApplicationConfig {
	return a.applicationConfig
}

func (a *Application) TemplatesEvaluator() *templates.Evaluator {
	return a.templatesEvaluator
}

func (a *Application) GalleryService() *services.GalleryService {
	return a.galleryService
}

func (a *Application) AgentJobService() *services.AgentJobService {
	return a.agentJobService
}

func (a *Application) AgentPoolService() *services.AgentPoolService {
	return a.agentPoolService
}

// StartupConfig returns the original startup configuration (from env vars, before file loading)
func (a *Application) StartupConfig() *config.ApplicationConfig {
	return a.startupConfig
}

func (a *Application) start() error {
	galleryService := services.NewGalleryService(a.ApplicationConfig(), a.ModelLoader())
	err := galleryService.Start(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ApplicationConfig().SystemState)
	if err != nil {
		return err
	}

	a.galleryService = galleryService

	// Initialize agent job service
	agentJobService := services.NewAgentJobService(
		a.ApplicationConfig(),
		a.ModelLoader(),
		a.ModelConfigLoader(),
		a.TemplatesEvaluator(),
	)

	err = agentJobService.Start(a.ApplicationConfig().Context)
	if err != nil {
		return err
	}

	a.agentJobService = agentJobService

	// Initialize agent pool service (LocalAGI integration)
	if a.applicationConfig.AgentPool.Enabled {
		aps, err := services.NewAgentPoolService(a.applicationConfig)
		if err == nil {
			if err := aps.Start(a.applicationConfig.Context); err != nil {
				xlog.Error("Failed to start agent pool", "error", err)
			} else {
				a.agentPoolService = aps
			}
		} else {
			xlog.Error("Failed to create agent pool service", "error", err)
		}
	}

	return nil
}
