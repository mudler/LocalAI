package application

import (
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/utils"
)

// declaredModelFiles resolves the files a model needs on disk beyond the ones
// its config names: what its gallery install or import declared under
// `files:`, and what the config itself lists under download_files. The
// distributed router stages these to workers, which cannot see the frontend's
// models directory.
func declaredModelFiles(configLoader *config.ModelConfigLoader, modelsPath string) func(modelName string) []string {
	return func(modelName string) []string {
		files := gallery.InstalledModelFiles(modelsPath, modelName)
		if cfg, ok := configLoader.GetModelConfig(modelName); ok {
			for _, f := range cfg.DownloadFiles {
				if utils.VerifyPath(f.Filename, modelsPath) != nil {
					continue
				}
				files = append(files, filepath.Join(modelsPath, f.Filename))
			}
		}
		return files
	}
}
