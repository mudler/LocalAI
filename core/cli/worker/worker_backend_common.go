package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// findBackendPath resolves the directory containing a backend's run.sh,
// installing the backend from the gallery if it isn't present.
// `name` is the gallery entry name (for vLLM the meta entry "vllm"
// resolves to a platform-specific package via capability lookup).
func findBackendPath(name, galleries string, systemState *system.SystemState) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		return "", err
	}
	if backend, ok := backends.Get(name); ok {
		return runFileDir(backend.RunFile)
	}

	ml := model.NewModelLoader(systemState)
	var gals []config.Gallery
	if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
		xlog.Error("failed loading galleries", "error", err)
		return "", err
	}
	if err := gallery.InstallBackendFromGallery(context.Background(), gals, systemState, ml, name, nil, true); err != nil {
		xlog.Error("backend not found, failed to install it", "name", name, "error", err)
		return "", err
	}

	backends, err = gallery.ListSystemBackends(systemState)
	if err != nil {
		return "", err
	}
	backend, ok := backends.Get(name)
	if !ok {
		return "", fmt.Errorf("%s backend not found after install", name)
	}
	return runFileDir(backend.RunFile)
}

func runFileDir(runFile string) (string, error) {
	dir := filepath.Dir(runFile)
	if dir == "" {
		return "", errors.New("backend has no run.sh, install it first")
	}
	return dir, nil
}
