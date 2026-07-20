package gallery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/xlog"
)

type ArtifactMaterializer interface {
	Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error)
}

type installOptions struct {
	materializer ArtifactMaterializer
	variant      string
}

type InstallOption func(*installOptions)

func WithArtifactMaterializer(materializer ArtifactMaterializer) InstallOption {
	return func(options *installOptions) {
		if materializer != nil {
			options.materializer = materializer
		}
	}
}

// WithVariant pins a gallery entry to a specific variant by name, bypassing
// hardware-based selection. The entry's own name is a valid pin, since the
// entry is itself the last resort. Ignored for entries that declare no
// variants.
func WithVariant(variant string) InstallOption {
	return func(options *installOptions) {
		options.variant = variant
	}
}

func applyInstallOptions(options ...InstallOption) installOptions {
	result := installOptions{materializer: modelartifacts.NewDefaultManager()}
	for _, option := range options {
		option(&result)
	}
	return result
}

func bindPrimaryArtifact(ctx context.Context, modelsPath string, typed *config.ModelConfig, configMap map[string]any, materializer ArtifactMaterializer, artifactSpec modelartifacts.Spec, inferred bool) (bool, error) {
	result, err := materializer.Ensure(ctx, modelsPath, artifactSpec)
	if err != nil {
		if inferred {
			xlog.Warn("falling back to legacy model loading after artifact materialization failed", "model", typed.Name, "error", err)
			return false, nil
		}
		return false, fmt.Errorf("materialize primary model artifact: %w", err)
	}
	next := []modelartifacts.Spec{result.Spec}
	if len(typed.Artifacts) > 1 {
		next = append(next, typed.Artifacts[1:]...)
	}
	typed.Artifacts = next
	// Companions are always explicit, so there is no legacy path to degrade to:
	// failing here leaves any previously installed config untouched rather than
	// persisting a half-acquired model that would fail later inside the backend.
	for i := 1; i < len(typed.Artifacts); i++ {
		if typed.Artifacts[i].Target != modelartifacts.TargetCompanion {
			continue
		}
		companion, err := materializer.Ensure(ctx, modelsPath, typed.Artifacts[i])
		if err != nil {
			return false, fmt.Errorf("materialize companion artifact %q: %w", typed.Artifacts[i].Name, err)
		}
		typed.Artifacts[i] = companion.Spec
	}
	artifactYAML, err := yaml.Marshal(typed.Artifacts)
	if err != nil {
		return false, err
	}
	var artifactValue any
	if err := yaml.Unmarshal(artifactYAML, &artifactValue); err != nil {
		return false, err
	}
	configMap["artifacts"] = artifactValue
	return true, nil
}

func writeModelConfigAtomic(fileName string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(fileName), ".model-config-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryName, 0644); err != nil {
		return err
	}
	return os.Rename(temporaryName, fileName)
}
