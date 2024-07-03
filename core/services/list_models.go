package services

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type ListModels struct {
	bcl       *config.BackendConfigLoader
	ml        *model.ModelLoader
	appConfig *config.ApplicationConfig
}

type LooseFilePolicy int

const (
	SKIP_IF_CONFIGURED LooseFilePolicy = iota
	SKIP_ALWAYS
	ALWAYS_INCLUDE
)

func NewListModels(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *ListModels {
	return &ListModels{
		bcl:       bcl,
		ml:        ml,
		appConfig: appConfig,
	}
}

func (lms *ListModels) ListModels(filter config.BackendConfigFilterFn, looseFilePolicy LooseFilePolicy) ([]schema.OpenAIModel, error) {

	var mm map[string]interface{} = map[string]interface{}{}

	dataModels := []schema.OpenAIModel{}

	// Start with the known configurations
	for _, c := range lms.bcl.GetBackendConfigsByFilter(filter) {
		log.Debug().Str("c.Backend", c.Backend).Str("c.Name", c.Name).Msg("LMS GetBackendConfigsByFilter")
		if looseFilePolicy == SKIP_IF_CONFIGURED {
			mm[c.Model] = nil
		}
		dataModels = append(dataModels, schema.OpenAIModel{ID: c.Name, Object: "model"})
	}

	if looseFilePolicy != SKIP_ALWAYS {
		// Then iterate through the loose files if requested.
		models, err := lms.ml.ListModels()
		if err != nil {
			return nil, err
		}

		for _, m := range models {
			// And only adds them if they shouldn't be skipped.
			if _, exists := mm[m]; !exists && filter(m, nil) {
				dataModels = append(dataModels, schema.OpenAIModel{ID: m, Object: "model"})
			}
		}
	}

	return dataModels, nil
}

func (lms *ListModels) CheckExistence(modelName string, looseFilePolicy LooseFilePolicy) (bool, error) {
	filter, err := config.BuildNameFilterFn(modelName)
	if err != nil {
		return false, err
	}
	models, err := lms.ListModels(filter, looseFilePolicy)
	if err != nil {
		return false, err
	}
	return (len(models) > 0), nil

}
