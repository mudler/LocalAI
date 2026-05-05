package worker

import (
	"os/exec"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/system"
)

const mlxDistributedGalleryName = "mlx-distributed"

func findMLXDistributedBackendPath(galleries string, systemState *system.SystemState) (string, error) {
	return findBackendPath(mlxDistributedGalleryName, galleries, systemState)
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
