package services

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
)

type LooseFilePolicy int

const (
	SKIP_IF_CONFIGURED LooseFilePolicy = iota
	SKIP_ALWAYS
	ALWAYS_INCLUDE
	LOOSE_ONLY
)

func ListModels(bcl *config.BackendConfigLoader, ml *model.ModelLoader, filter config.BackendConfigFilterFn, looseFilePolicy LooseFilePolicy) ([]string, error) {

	var skipMap map[string]interface{} = map[string]interface{}{}

	dataModels := []string{}

	// Start with known configurations
	if looseFilePolicy != LOOSE_ONLY {
		for _, c := range bcl.GetBackendConfigsByFilter(filter) {
			if looseFilePolicy == SKIP_IF_CONFIGURED {
				skipMap[c.Model] = nil
			}
			dataModels = append(dataModels, c.Name)
		}
	}

	// Then iterate through the loose files if requested.
	if looseFilePolicy != SKIP_ALWAYS {

		models, err := ml.ListFilesInModelPath()
		if err != nil {
			return nil, err
		}
		for _, m := range models {
			// And only adds them if they shouldn't be skipped.
			if _, exists := skipMap[m]; !exists && filter(m, nil) {
				dataModels = append(dataModels, m)
			}
		}
	}

	return dataModels, nil
}
