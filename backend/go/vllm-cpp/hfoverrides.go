package main

// hf_overrides, vLLM parity: a JSON object of config.json keys laid over the
// model directory's own config.json at load time.
//
// The engine reads config.json straight from the model directory and has no
// override input on the C ABI, so the only way to change what it sees without
// editing the snapshot is to hand it a different directory. The overlay built
// here is that directory: a private temp dir holding the merged config.json
// and a symlink for every other entry of the original. The snapshot itself is
// never written, which matters because it is a content-addressed download that
// a gallery reinstall or a hash check would otherwise flag or overwrite.
//
// The canonical use is opting a published checkpoint into an engine adapter
// its config does not name, e.g. {"architectures": ["Tev1Model"]} on a Tev1
// snapshot that declares Qwen3_5ForConditionalGeneration.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// newConfigOverlay builds the overlay directory for modelDir with overrides
// merged over its config.json and returns its path. Keys are merged at the top
// level only: an override replaces the whole value of its key, nested objects
// included, which is what vLLM does for a plain (non sub-config) key.
//
// Bad input is refused rather than skipped, unlike an unknown engine_args key:
// hf_overrides exists to change which architecture loads, so silently loading
// the unmodified config would serve a different model than the one configured.
func newConfigOverlay(modelDir, overrides string) (dir string, err error) {
	var patch map[string]any
	if err := json.Unmarshal([]byte(overrides), &patch); err != nil || patch == nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides must be a JSON object of config.json keys, got %q", overrides)
	}

	info, err := os.Stat(modelDir)
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("vllm-cpp: hf_overrides needs a model directory with a config.json, %q is a file", modelDir)
	}
	absDir, err := filepath.Abs(modelDir)
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: %w", err)
	}

	raw, err := os.ReadFile(filepath.Join(absDir, "config.json"))
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides needs %s: %w", filepath.Join(absDir, "config.json"), err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil || config == nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: %s is not a JSON object", filepath.Join(absDir, "config.json"))
	}
	for k, v := range patch {
		config[k] = v
	}
	merged, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: encoding the merged config.json: %w", err)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: %w", err)
	}

	dir, err = os.MkdirTemp("", "vllm-cpp-hf-overrides-*")
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: creating the overlay: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
			dir = ""
		}
	}()

	// Links point at the entry path, not at what it resolves to: an HF cache
	// snapshot is itself a tree of links into blobs/, and the engine already
	// follows those.
	for _, e := range entries {
		if e.Name() == "config.json" {
			continue
		}
		if err := os.Symlink(filepath.Join(absDir, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return "", fmt.Errorf("vllm-cpp: hf_overrides: linking %s into the overlay: %w", e.Name(), err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), merged, 0o600); err != nil {
		return "", fmt.Errorf("vllm-cpp: hf_overrides: writing the merged config.json: %w", err)
	}
	return dir, nil
}

// removeConfigOverlay deletes an overlay built by newConfigOverlay. RemoveAll
// removes the symlinks themselves and never descends into their targets, so
// the original model directory is safe.
func removeConfigOverlay(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vllm-cpp: removing the hf_overrides overlay %s: %w", dir, err)
	}
	return nil
}

// hasHFOverrides reports whether an overlay is needed. An empty object is a
// no-op in vLLM too, so it loads the directory directly instead of paying for
// an overlay that changes nothing.
func hasHFOverrides(overrides string) bool {
	switch strings.TrimSpace(overrides) {
	case "", "{}", "null":
		return false
	}
	return true
}
