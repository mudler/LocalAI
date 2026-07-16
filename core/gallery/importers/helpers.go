package importers

import (
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"gopkg.in/yaml.v3"
)

var managedArtifactBackends = map[string]struct{}{
	"transformers": {}, "huggingface-embeddings": {}, "sentencetransformers": {},
	"transformers-musicgen": {}, "mamba": {}, "diffusers": {}, "qwen-asr": {},
	"fish-speech": {}, "nemo": {}, "voxcpm": {}, "qwen-tts": {},
	"liquid-audio": {}, "vllm": {}, "vllm-omni": {}, "sglang": {},
}

// AttachPrimaryArtifact adds the controller-managed source only when the
// importer selected the same repository and a migrated backend.
func AttachPrimaryArtifact(model gallery.ModelConfig, details Details) (gallery.ModelConfig, error) {
	if len(model.Files) != 0 || details.HuggingFace == nil || details.HuggingFace.ModelID == "" {
		return model, nil
	}
	var cfg config.ModelConfig
	if err := yaml.Unmarshal([]byte(model.ConfigFile), &cfg); err != nil {
		return gallery.ModelConfig{}, err
	}
	if _, supported := managedArtifactBackends[cfg.Backend]; !supported {
		return model, nil
	}
	if len(cfg.Artifacts) != 0 || cfg.Model != details.HuggingFace.ModelID {
		return model, nil
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(model.ConfigFile), &document); err != nil {
		return gallery.ModelConfig{}, err
	}
	document["artifacts"] = []map[string]any{{
		"name":   modelartifacts.TargetModel,
		"target": modelartifacts.TargetModel,
		"source": map[string]any{
			"type": modelartifacts.SourceTypeHuggingFace,
			"repo": details.HuggingFace.ModelID,
		},
	}}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	model.ConfigFile = string(encoded)
	return model, nil
}

// LocalModelPath normalizes a model URI for backends that treat the model
// field as a HuggingFace repo id or local filesystem path (mlx, mlx-vlm,
// vllm, transformers, diffusers). A "file://" import URI is reduced to the
// bare path it points at: mlx-lm and vLLM otherwise mis-read the "file://"
// scheme as a repo id and fail with "Repo id must be in the form
// 'repo_name' or 'namespace/repo_name'" (issue #7461). HuggingFace and HTTP
// URIs are returned unchanged so the existing remote-load path is untouched.
func LocalModelPath(uri string) string {
	if path, ok := strings.CutPrefix(uri, downloader.LocalPrefix); ok {
		return path
	}
	return uri
}

// HasFile returns true when any file in files has exactly the given basename.
// Directory components in file.Path are ignored — a nested
// "sub/dir/config.json" is considered a match for name = "config.json".
func HasFile(files []hfapi.ModelFile, name string) bool {
	for _, f := range files {
		if filepath.Base(f.Path) == name {
			return true
		}
	}
	return false
}

// HasExtension returns true when any file has the given extension
// (case-insensitive). ext must include the leading dot, e.g. ".onnx".
func HasExtension(files []hfapi.ModelFile, ext string) bool {
	lower := strings.ToLower(ext)
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Path), lower) {
			return true
		}
	}
	return false
}

// HasONNX returns true when any file ends in .onnx (case-insensitive).
func HasONNX(files []hfapi.ModelFile) bool {
	return HasExtension(files, ".onnx")
}

// HasONNXConfigPair returns true when an .onnx file has an accompanying
// "<same basename>.onnx.json" file. This is the piper voice packaging
// convention, e.g. en_US-amy-medium.onnx + en_US-amy-medium.onnx.json.
func HasONNXConfigPair(files []hfapi.ModelFile) bool {
	paths := make(map[string]struct{}, len(files))
	for _, f := range files {
		paths[strings.ToLower(f.Path)] = struct{}{}
	}
	for p := range paths {
		if !strings.HasSuffix(p, ".onnx") {
			continue
		}
		if _, ok := paths[p+".json"]; ok {
			return true
		}
	}
	return false
}

// HFOwnerRepoFromURI extracts the "owner", "repo" pair from an HF URI.
// Accepted prefixes: "https://huggingface.co/", "huggingface://", "hf://".
// Returns ok=false when the URI is not an HF URI or is missing either
// component. This exists so importers can fall back to URI-based matching
// when pkg/huggingface-api's recursive tree listing errors out on repos
// with nested subdirectories (a known pre-existing bug).
func HFOwnerRepoFromURI(uri string) (owner, repo string, ok bool) {
	stripped := uri
	for _, pfx := range []string{"https://huggingface.co/", "huggingface://", "hf://"} {
		stripped = strings.TrimPrefix(stripped, pfx)
	}
	parts := strings.SplitN(stripped, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// HasGGMLFile returns true when any file matches "<prefix>*.bin", which is
// the whisper.cpp packaging convention (e.g. "ggml-base.en.bin"). Both prefix
// and suffix match is case-sensitive on prefix and case-insensitive on the
// .bin extension.
func HasGGMLFile(files []hfapi.ModelFile, prefix string) bool {
	for _, f := range files {
		name := filepath.Base(f.Path)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if strings.HasSuffix(strings.ToLower(name), ".bin") {
			return true
		}
	}
	return false
}
