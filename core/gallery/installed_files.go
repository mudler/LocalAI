package gallery

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// InstalledModelFiles returns the absolute paths of the files that the install
// of model name declared (the entry's `files:`), as recorded in its gallery
// file. A model config names only the file a backend opens first, while a
// backend can read more by itself (llama.cpp opens the other shards of a split
// GGUF by name), so this is the complete list of what the model needs on disk.
// It returns nil for a model that was not installed from a gallery or import.
func InstalledModelFiles(modelsPath, name string) []string {
	// Model names can hold path separators; the gallery file flattens them
	// the same way listModelFiles does.
	rel := galleryFileName(strings.ReplaceAll(name, string(os.PathSeparator), "__"))
	if err := utils.VerifyPath(rel, modelsPath); err != nil {
		return nil
	}
	galleryFile := filepath.Join(modelsPath, rel)
	if _, err := os.Stat(galleryFile); err != nil {
		return nil
	}
	cfg, err := ReadConfigFile[ModelConfig](galleryFile)
	if err != nil {
		xlog.Warn("Failed to read gallery file for installed model files", "model", name, "file", galleryFile, "error", err)
		return nil
	}

	files := make([]string, 0, len(cfg.Files))
	for _, f := range cfg.Files {
		// VerifyPath joins its argument onto modelsPath itself, so it must
		// get the relative name; an absolute path would always pass.
		if err := utils.VerifyPath(f.Filename, modelsPath); err != nil {
			xlog.Warn("Ignoring declared model file outside the models path", "model", name, "file", f.Filename)
			continue
		}
		files = append(files, filepath.Join(modelsPath, f.Filename))
	}
	return files
}
