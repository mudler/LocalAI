package importers

import (
	"path/filepath"
	"strings"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

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
