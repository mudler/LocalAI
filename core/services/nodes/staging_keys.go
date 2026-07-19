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
// ModelFile remote path and that file's path relative to the frontend models
// directory. Returns the ModelPath that makes all relative option values
// resolve correctly.
//
// Example:
//
//	remotePath = "/worker/models/my-model/sd-cpp/models/flux.gguf"
//	relative   = "sd-cpp/models/flux.gguf"
//	→ returns    "/worker/models/my-model"
func DeriveRemoteModelPath(remotePath, relative string) string {
	return filepath.Clean(strings.TrimSuffix(remotePath, relative))
}

// relativeToModelsDir returns the staged file's path relative to the frontend
// models directory, which is the suffix DeriveRemoteModelPath strips to recover
// the worker's models root. It falls back to the Model field for the callers
// that predate a resolvable models directory.
func relativeToModelsDir(frontendModelsDir, localPath, model string) string {
	if frontendModelsDir != "" {
		if rel, err := filepath.Rel(frontendModelsDir, localPath); err == nil &&
			!strings.HasPrefix(rel, "..") && rel != "." {
			return rel
		}
	}
	return model
}

// managedArtifactDir is the root of the content-addressed artifact tree, kept
// in sync with the layout modelartifacts.LayoutFor builds.
const managedArtifactDir = ".artifacts"

// ModelsRootForModelFile finds the frontend models directory a model file sits
// under.
//
// The legacy answer is to strip the Model relative path off the end of
// ModelFile. That silently degrades for a managed artifact, whose ModelFile is
// a content-addressed snapshot directory while Model stays a bare HuggingFace
// repo id: nothing matches, the suffix strip is a no-op, and the "models
// directory" comes out as the snapshot itself. Everything anchored on it then
// narrows to that one snapshot, which is precisely where a sibling companion
// snapshot becomes unreachable and its files collapse to bare basenames.
//
// So when the path runs through the managed artifact tree, anchor on the
// directory that contains that tree instead.
func ModelsRootForModelFile(modelFile, model string) string {
	if modelFile == "" {
		return ""
	}
	parts := strings.Split(filepath.Clean(modelFile), string(filepath.Separator))
	for i, part := range parts {
		if part == managedArtifactDir && i > 0 {
			return filepath.Clean(strings.Join(parts[:i], string(filepath.Separator)))
		}
	}
	if model == "" {
		return ""
	}
	return filepath.Clean(strings.TrimSuffix(modelFile, model))
}
