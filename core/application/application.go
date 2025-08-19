package application

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
)

type Application struct {
	backendLoader      *config.ModelConfigLoader
	modelLoader        *model.ModelLoader
	applicationConfig  *config.ApplicationConfig
	templatesEvaluator *templates.Evaluator
	galleryService     *services.GalleryService
}

func newApplication(appConfig *config.ApplicationConfig) *Application {
	return &Application{
		backendLoader:      config.NewModelConfigLoader(appConfig.SystemState.Model.ModelsPath),
		modelLoader:        model.NewModelLoader(appConfig.SystemState, appConfig.SingleBackend),
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

func (a *Application) start() error {
	galleryService := services.NewGalleryService(a.ApplicationConfig(), a.ModelLoader())
	err := galleryService.Start(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ApplicationConfig().SystemState)
	if err != nil {
		return err
	}

	a.galleryService = galleryService

	return nil
}
