package application

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/templates"
)

type Application struct {
	backendLoader      *config.BackendConfigLoader
	modelLoader        *model.ModelLoader
	applicationConfig  *config.ApplicationConfig
	templatesEvaluator *templates.Evaluator
}

func newApplication(appConfig *config.ApplicationConfig) *Application {
	return &Application{
		backendLoader:      config.NewBackendConfigLoader(appConfig.ModelPath),
		modelLoader:        model.NewModelLoader(appConfig.ModelPath),
		applicationConfig:  appConfig,
		templatesEvaluator: templates.NewEvaluator(appConfig.ModelPath),
	}
}

func (a *Application) BackendLoader() *config.BackendConfigLoader {
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
