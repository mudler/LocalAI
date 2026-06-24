package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/xlog"
)

// DeleteStagedModelFiles removes all staged files for a model from a worker's
// models directory. Files are expected to be in a subdirectory named after the
// model's tracking key (created by stageModelFiles in the router).
//
// Workers receive model files via S3/HTTP file staging, not gallery install,
// so they lack the YAML configs that DeleteModelFromSystem requires.
//
// Falls back to glob-based cleanup for single-file models or legacy layouts.
func DeleteStagedModelFiles(modelsPath, modelName string) error {
	if modelName == "" {
		return fmt.Errorf("empty model name")
	}

	// Clean and validate: resolved path must stay within modelsPath
	modelPath := filepath.Clean(filepath.Join(modelsPath, modelName))
	absModels := filepath.Clean(modelsPath)
	if !strings.HasPrefix(modelPath, absModels+string(filepath.Separator)) {
		return fmt.Errorf("model name %q escapes models directory", modelName)
	}

	// Primary: remove the model's subdirectory (contains all staged files)
	if info, err := os.Stat(modelPath); err == nil && info.IsDir() {
		return os.RemoveAll(modelPath)
	}

	// Fallback for single-file models or legacy layouts:
	// remove exact file match + glob siblings
	removed := false
	if _, err := os.Stat(modelPath); err == nil {
		if err := os.Remove(modelPath); err != nil {
			xlog.Warn("Failed to remove model file", "path", modelPath, "error", err)
		} else {
			removed = true
		}
	}

	// Remove sibling files (e.g., model.gguf.mmproj alongside model.gguf)
	matches, _ := filepath.Glob(modelPath + ".*")
	for _, m := range matches {
		clean := filepath.Clean(m)
		if !strings.HasPrefix(clean, absModels+string(filepath.Separator)) {
			continue // skip any glob result that escapes
		}
		if err := os.Remove(clean); err != nil {
			xlog.Warn("Failed to remove model-related file", "path", clean, "error", err)
		} else {
			removed = true
		}
	}

	if !removed {
		xlog.Debug("No files found to delete for model", "model", modelName, "path", modelPath)
	}
	return nil
}
