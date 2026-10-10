package galleryop

import (
	"context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/model"
)

type LooseFilePolicy int

const (
	LOOSE_ONLY LooseFilePolicy = iota
	SKIP_IF_CONFIGURED
	SKIP_ALWAYS
	ALWAYS_INCLUDE
)

// discoveryFiles is a call-local seam for verifying discovery operation counts.
type discoveryFiles interface {
	ListFilesInModelPathContext(context.Context) ([]string, error)
	ExistsInModelPath(string) bool
}

func ListModels(bcl *config.ModelConfigLoader, ml *model.ModelLoader, filter config.ModelConfigFilterFn, looseFilePolicy LooseFilePolicy) ([]string, error) {
	return ListModelsContext(context.Background(), bcl, ml, filter, looseFilePolicy)
}

// ListModelsContext carries diagnostics through config and loose-file discovery.
func ListModelsContext(ctx context.Context, bcl *config.ModelConfigLoader, ml *model.ModelLoader, filter config.ModelConfigFilterFn, looseFilePolicy LooseFilePolicy) ([]string, error) {
	return listModelsContext(ctx, bcl, ml, filter, looseFilePolicy)
}

func listModelsContext(ctx context.Context, bcl *config.ModelConfigLoader, ml discoveryFiles, filter config.ModelConfigFilterFn, looseFilePolicy LooseFilePolicy) ([]string, error) {

	// Callers (e.g. the Ollama /api/tags handler) pass nil to mean "no
	// filtering". Without this guard the loose-file loop below dereferences
	// filter and panics, which Echo surfaces to clients as a dropped
	// connection (see issue #9817).
	if filter == nil {
		filter = config.NoFilterFn
	}

	skipMap := map[string]struct{}{}

	dataModels := []string{}

	// Start with known configurations

	configs := bcl.GetModelConfigsByFilterContext(ctx, filter)
	end := diagnostics.Begin(ctx, diagnostics.PhaseLooseFilter)
	for _, c := range configs {
		// Is this better than looseFilePolicy <= SKIP_IF_CONFIGURED ? less performant but more readable?
		if (looseFilePolicy == SKIP_IF_CONFIGURED) || (looseFilePolicy == LOOSE_ONLY) {
			skipMap[c.Model] = struct{}{}
		}
		if looseFilePolicy != LOOSE_ONLY {
			dataModels = append(dataModels, c.Name)
		}
	}

	end(diagnostics.OutcomeOK, len(configs))

	// Then iterate through the loose files if requested.
	if looseFilePolicy != SKIP_ALWAYS {

		models, err := ml.ListFilesInModelPathContext(ctx)
		if err != nil {
			return nil, err
		}
		end = diagnostics.Begin(ctx, diagnostics.PhaseLooseFilter)
		for _, m := range models {
			// And only adds them if they shouldn't be skipped.
			if _, exists := skipMap[m]; !exists && filter(m, nil) {
				dataModels = append(dataModels, m)
			}
		}
		end(diagnostics.OutcomeOK, len(models))
	}

	return dataModels, nil
}

func CheckIfModelExists(bcl *config.ModelConfigLoader, ml *model.ModelLoader, modelName string, looseFilePolicy LooseFilePolicy) (bool, error) {
	return CheckIfModelExistsContext(context.Background(), bcl, ml, modelName, looseFilePolicy)
}

// CheckIfModelExistsContext observes the existing lookup and weight-file fallback.
func CheckIfModelExistsContext(ctx context.Context, bcl *config.ModelConfigLoader, ml *model.ModelLoader, modelName string, looseFilePolicy LooseFilePolicy) (bool, error) {
	return checkIfModelExistsContext(ctx, bcl, ml, modelName, looseFilePolicy)
}

func checkIfModelExistsContext(ctx context.Context, bcl *config.ModelConfigLoader, ml discoveryFiles, modelName string, looseFilePolicy LooseFilePolicy) (bool, error) {
	filter, err := config.BuildNameFilterFn(modelName)
	if err != nil {
		return false, err
	}
	models, err := listModelsContext(ctx, bcl, ml, filter, looseFilePolicy)
	if err != nil {
		return false, err
	}
	if len(models) > 0 {
		return true, nil
	}

	// ListModels may not find raw model weight files (e.g. .ggml, .gguf)
	// because ListFilesInModelPath skips known weight-file extensions.
	// Fall back to checking if the file exists directly in the model path.
	end := diagnostics.Begin(ctx, diagnostics.PhaseExistenceFallback)
	exists := ml.ExistsInModelPath(modelName)
	// ExistsInModelPath exposes only a boolean, not an underlying filesystem error.
	end(diagnostics.OutcomeOK, 1)
	if exists {
		return true, nil
	}

	return false, nil
}
