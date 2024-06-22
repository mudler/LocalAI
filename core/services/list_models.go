package services

import (
	"regexp"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

type ListModelsService struct {
	bcl       *config.BackendConfigLoader
	ml        *model.ModelLoader
	appConfig *config.ApplicationConfig
}

func NewListModelsService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *ListModelsService {
	return &ListModelsService{
		bcl:       bcl,
		ml:        ml,
		appConfig: appConfig,
	}
}

func (lms *ListModelsService) ListModels(filter string, excludeConfigured bool) ([]schema.OpenAIModel, error) {

	models, err := lms.ml.ListModels()
	if err != nil {
		return nil, err
	}

	var mm map[string]interface{} = map[string]interface{}{}

	dataModels := []schema.OpenAIModel{}

	var filterFn func(name string) bool

	// If filter is not specified, do not filter the list by model name
	if filter == "" {
		filterFn = func(_ string) bool { return true }
	} else {
		// If filter _IS_ specified, we compile it to a regex which is used to create the filterFn
		rxp, err := regexp.Compile(filter)
		if err != nil {
			return nil, err
		}
		filterFn = func(name string) bool {
			return rxp.MatchString(name)
		}
	}

	// Start with the known configurations
	for _, c := range lms.bcl.GetAllBackendConfigs() {
		if excludeConfigured {
			mm[c.Model] = nil
		}

		if filterFn(c.Name) {
			dataModels = append(dataModels, schema.OpenAIModel{ID: c.Name, Object: "model"})
		}
	}

	// Then iterate through the loose files:
	for _, m := range models {
		// And only adds them if they shouldn't be skipped.
		if _, exists := mm[m]; !exists && filterFn(m) {
			dataModels = append(dataModels, schema.OpenAIModel{ID: m, Object: "model"})
		}
	}

	return dataModels, nil
}
