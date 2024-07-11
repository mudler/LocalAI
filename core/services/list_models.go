package services

import (
	"regexp"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
)

func ListModels(bcl *config.BackendConfigLoader, ml *model.ModelLoader, filter string, excludeConfigured bool) ([]string, error) {

	models, err := ml.ListFilesInModelPath()
	if err != nil {
		return nil, err
	}

	var mm map[string]interface{} = map[string]interface{}{}

	dataModels := []string{}

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
	for _, c := range bcl.GetAllBackendConfigs() {
		if excludeConfigured {
			mm[c.Model] = nil
		}

		if filterFn(c.Name) {
			dataModels = append(dataModels, c.Name)
		}
	}

	// Then iterate through the loose files:
	for _, m := range models {
		// And only adds them if they shouldn't be skipped.
		if _, exists := mm[m]; !exists && filterFn(m) {
			dataModels = append(dataModels, m)
		}
	}

	return dataModels, nil
}
