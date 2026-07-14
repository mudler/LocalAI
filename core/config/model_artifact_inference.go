package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

// PrimaryArtifactSpec returns the managed primary artifact to materialize for
// this config. The boolean return is false when the config should stay on the
// legacy path.
func (c ModelConfig) PrimaryArtifactSpec(modelsPath string) (modelartifacts.Spec, bool, bool, error) {
	if len(c.Artifacts) > 0 {
		return c.Artifacts[0], false, true, nil
	}

	modelRef := strings.TrimSpace(c.Model)
	if modelRef == "" {
		return modelartifacts.Spec{}, false, false, nil
	}
	if len(c.DownloadFiles) > 0 {
		return modelartifacts.Spec{}, false, false, nil
	}

	if modelsPath != "" {
		for _, candidate := range []string{
			modelRef,
			filepath.Join(modelsPath, modelRef),
		} {
			if info, err := os.Stat(candidate); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
				return modelartifacts.Spec{}, false, false, nil
			}
		}
	}

	spec, ok, err := modelartifacts.ParsePrimaryReference(modelRef)
	if err != nil {
		return modelartifacts.Spec{}, false, false, err
	}
	if !ok {
		if strings.Contains(modelRef, "/") && !strings.HasPrefix(modelRef, ".") && !filepath.IsAbs(modelRef) {
			repo, err := modelartifacts.CanonicalRepo(modelRef)
			if err != nil {
				if strings.Count(modelRef, "/") == 1 {
					return modelartifacts.Spec{}, false, false, fmt.Errorf("invalid Hugging Face reference %q: %w", modelRef, err)
				}
				return modelartifacts.Spec{}, false, false, nil
			}
			return modelartifacts.Spec{
				Name:   modelartifacts.TargetModel,
				Target: modelartifacts.TargetModel,
				Source: modelartifacts.Source{Type: modelartifacts.SourceTypeHuggingFace, Repo: repo},
			}, true, true, nil
		}
		return modelartifacts.Spec{}, false, false, nil
	}
	return spec, true, true, nil
}
