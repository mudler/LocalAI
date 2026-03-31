package nodes

import (
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/storage"
)

// StagingKeyMapper generates storage keys for model files, namespaced under
// a tracking key. It preserves the subdirectory structure relative to the
// frontend's model directory so that backends can resolve relative paths
// (generic options, vae_path, etc.) via ModelPath.
type StagingKeyMapper struct {
	TrackingKey       string
	FrontendModelsDir string
}

// Key generates the storage key for a local file path. The key is namespaced
// under TrackingKey while preserving the path relative to FrontendModelsDir.
// Falls back to basename if the file is outside FrontendModelsDir.
func (m *StagingKeyMapper) Key(localPath string) string {
	if m.FrontendModelsDir != "" {
		if rel, err := filepath.Rel(m.FrontendModelsDir, localPath); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return storage.ModelKey(filepath.Join(m.TrackingKey, rel))
		}
	}
	return storage.ModelKey(filepath.Join(m.TrackingKey, filepath.Base(localPath)))
}

// DeriveRemoteModelPath computes the worker's ModelPath from the staged
// ModelFile remote path and the original Model relative path.
// Returns the ModelPath that makes all relative option values resolve correctly.
//
// Example:
//
//	remotePath = "/worker/models/my-model/sd-cpp/models/flux.gguf"
//	model      = "sd-cpp/models/flux.gguf"
//	→ returns    "/worker/models/my-model"
func DeriveRemoteModelPath(remotePath, model string) string {
	return filepath.Clean(strings.TrimSuffix(remotePath, model))
}
