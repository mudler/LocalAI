package services

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
)

type LooseFilePolicy int

const (
	LOOSE_ONLY LooseFilePolicy = iota
	SKIP_IF_CONFIGURED
	SKIP_ALWAYS
	ALWAYS_INCLUDE
)

func ListModels(bcl *config.ModelConfigLoader, ml *model.ModelLoader, filter config.ModelConfigFilterFn, looseFilePolicy LooseFilePolicy) ([]string, error) {

	var skipMap map[string]interface{} = map[string]interface{}{}

	dataModels := []string{}

	// Start with known configurations

	for _, c := range bcl.GetModelConfigsByFilter(filter) {
		// Is this better than looseFilePolicy <= SKIP_IF_CONFIGURED ? less performant but more readable?
		if (looseFilePolicy == SKIP_IF_CONFIGURED) || (looseFilePolicy == LOOSE_ONLY) {
			skipMap[c.Model] = nil
		}
		if looseFilePolicy != LOOSE_ONLY {
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

func CheckIfModelExists(bcl *config.ModelConfigLoader, ml *model.ModelLoader, modelName string, looseFilePolicy LooseFilePolicy) (bool, error) {
	filter, err := config.BuildNameFilterFn(modelName)
	if err != nil {
		return false, err
	}
	models, err := ListModels(bcl, ml, filter, looseFilePolicy)
	if err != nil {
		return false, err
	}
	return (len(models) > 0), nil
}
