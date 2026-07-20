package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/xlog"
)

// managedArtifactBackends is the set of backends that load a model from a
// snapshot *directory* (a HuggingFace repo consumed as a folder of weights,
// config, and tokenizer files). Only these backends may have a managed
// artifact *inferred* from a bare model reference: single-file backends such
// as llama.cpp or whisper would otherwise be handed the snapshot directory
// instead of the weight file and fail to load it. The importer relies on the
// same gate, so both paths agree on which backends auto-materialize.
var managedArtifactBackends = map[string]struct{}{
	"transformers": {}, "huggingface-embeddings": {}, "sentencetransformers": {},
	"transformers-musicgen": {}, "mamba": {}, "diffusers": {}, "qwen-asr": {},
	"fish-speech": {}, "nemo": {}, "voxcpm": {}, "qwen-tts": {},
	"liquid-audio": {}, "vllm": {}, "vllm-omni": {}, "sglang": {},
	"longcat-video": {},
}

// IsManagedArtifactBackend reports whether backend consumes a model as a
// snapshot directory and is therefore eligible for inferred artifact
// materialization. An explicit artifacts: block bypasses this gate; single-file
// resolution handles the load path for single-file backends in that case.
func IsManagedArtifactBackend(backend string) bool {
	_, ok := managedArtifactBackends[backend]
	return ok
}

// LogArtifactFallback records a degradation to the legacy loading path. For a
// backend that consumes a snapshot *directory*, "legacy loading" is not a
// graceful degradation: the backend downloads the whole repo itself, in-band,
// inside LoadModel, on every load. That reliably exceeds the remote-load
// deadline and surfaces as a bare timeout, so it is logged at error to survive
// a default log level and give the timeout a named cause (#10981).
//
// Today every inferred artifact already comes from such a backend, but the gate
// is checked explicitly so widening PrimaryArtifactSpec later cannot silently
// promote an ordinary fallback to an error.
func LogArtifactFallback(model, backend string, err error) {
	if IsManagedArtifactBackend(backend) {
		xlog.Error("artifact materialization failed; the backend will download the model in-band on every load",
			"model", model, "backend", backend, "error", err)
		return
	}
	xlog.Warn("falling back to legacy model loading after artifact materialization failed",
		"model", model, "backend", backend, "error", err)
}

// PrimaryArtifactSpec returns the managed primary artifact to materialize for
// this config. The boolean return is false when the config should stay on the
// legacy path.
func (c ModelConfig) PrimaryArtifactSpec(modelsPath string) (modelartifacts.Spec, bool, bool, error) {
	if len(c.Artifacts) > 0 {
		return c.Artifacts[0], false, true, nil
	}

	modelRef := strings.TrimSpace(c.Model)
	if modelRef == "" {
		return modelartifacts.Spec{}, false, false, nil
	}
	if len(c.DownloadFiles) > 0 {
		return modelartifacts.Spec{}, false, false, nil
	}
	// Only directory-consuming backends may have an artifact inferred from a
	// bare reference; single-file backends stay on the legacy download-to-file
	// path so the backend receives the weight file itself, not its directory.
	if !IsManagedArtifactBackend(c.Backend) {
		return modelartifacts.Spec{}, false, false, nil
	}

	if modelsPath != "" {
		for _, candidate := range []string{
			modelRef,
			filepath.Join(modelsPath, modelRef),
		} {
			if info, err := os.Stat(candidate); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
				return modelartifacts.Spec{}, false, false, nil
			}
		}
	}

	spec, ok, err := modelartifacts.ParsePrimaryReference(modelRef)
	if err != nil {
		return modelartifacts.Spec{}, false, false, err
	}
	if !ok {
		if strings.Contains(modelRef, "/") && !strings.HasPrefix(modelRef, ".") && !filepath.IsAbs(modelRef) {
			repo, err := modelartifacts.CanonicalRepo(modelRef)
			if err != nil {
				if strings.Count(modelRef, "/") == 1 {
					return modelartifacts.Spec{}, false, false, fmt.Errorf("invalid Hugging Face reference %q: %w", modelRef, err)
				}
				return modelartifacts.Spec{}, false, false, nil
			}
			return modelartifacts.Spec{
				Name:   modelartifacts.TargetModel,
				Target: modelartifacts.TargetModel,
				Source: modelartifacts.Source{Type: modelartifacts.SourceTypeHuggingFace, Repo: repo},
			}, true, true, nil
		}
		return modelartifacts.Spec{}, false, false, nil
	}
	return spec, true, true, nil
}
