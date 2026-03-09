package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

const mlxDistributedGalleryName = "mlx-distributed"

// findMLXDistributedBackendPath finds or installs the mlx-distributed backend
// and returns the directory containing run.sh.
func findMLXDistributedBackendPath(galleries string, systemState *system.SystemState) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		return "", err
	}

	backend, ok := backends.Get(mlxDistributedGalleryName)
	if !ok {
		ml := model.NewModelLoader(systemState)
		var gals []config.Gallery
		if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
			xlog.Error("failed loading galleries", "error", err)
			return "", err
		}
		if err := gallery.InstallBackendFromGallery(context.Background(), gals, systemState, ml, mlxDistributedGalleryName, nil, true); err != nil {
			xlog.Error("mlx-distributed backend not found, failed to install it", "error", err)
			return "", err
		}
		// Re-fetch after install
		backends, err = gallery.ListSystemBackends(systemState)
		if err != nil {
			return "", err
		}
		backend, ok = backends.Get(mlxDistributedGalleryName)
		if !ok {
			return "", errors.New("mlx-distributed backend not found after install")
		}
	}

	backendPath := filepath.Dir(backend.RunFile)
	if backendPath == "" {
		return "", errors.New("mlx-distributed backend not found, install it first")
	}
	return backendPath, nil
}

// buildMLXCommand builds the exec.Cmd to launch the mlx-distributed backend.
// backendPath is the directory containing run.sh (empty string to fall back to
// running backend.py directly via python3).
func buildMLXCommand(backendPath string, args ...string) *exec.Cmd {
	if backendPath != "" {
		return exec.Command(filepath.Join(backendPath, "run.sh"), args...)
	}
	return exec.Command("python3", append([]string{"backend.py"}, args...)...)
}
