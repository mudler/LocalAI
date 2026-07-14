package gallery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type ArtifactMaterializer interface {
	Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error)
}

type installOptions struct {
	materializer ArtifactMaterializer
}

type InstallOption func(*installOptions)

func WithArtifactMaterializer(materializer ArtifactMaterializer) InstallOption {
	return func(options *installOptions) {
		if materializer != nil {
			options.materializer = materializer
		}
	}
}

func applyInstallOptions(options ...InstallOption) installOptions {
	result := installOptions{materializer: modelartifacts.NewDefaultManager()}
	for _, option := range options {
		option(&result)
	}
	return result
}

func bindPrimaryArtifact(ctx context.Context, modelsPath string, typed *config.ModelConfig, configMap map[string]any, materializer ArtifactMaterializer) error {
	if len(typed.Artifacts) == 0 {
		return nil
	}
	result, err := materializer.Ensure(ctx, modelsPath, typed.Artifacts[0])
	if err != nil {
		return fmt.Errorf("materialize primary model artifact: %w", err)
	}
	typed.Artifacts[0] = result.Spec
	artifactYAML, err := yaml.Marshal(typed.Artifacts)
	if err != nil {
		return err
	}
	var artifactValue any
	if err := yaml.Unmarshal(artifactYAML, &artifactValue); err != nil {
		return err
	}
	configMap["artifacts"] = artifactValue
	return nil
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
