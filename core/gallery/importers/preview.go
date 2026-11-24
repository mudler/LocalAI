package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"gopkg.in/yaml.v3"
)

// ImportPreview is what an import of a repository would produce, without
// downloading or enqueueing anything.
type ImportPreview struct {
	// Backend is the backend the generated config selects.
	Backend string `json:"backend,omitempty"`
	// Importer is the canonical name of the importer that matched.
	Importer string `json:"importer,omitempty"`
	// Name is the model name the import would use.
	Name string `json:"name,omitempty"`
	// Files lists the files the import would download, as file names.
	Files []string `json:"files,omitempty"`
}

// PreviewImport runs the importer registry on repository details the caller
// already holds (a file listing, a pipeline tag) and reports what an import
// would do. It makes no network request: DiscoverModelConfig does the listing
// and then calls the same matching code. Preferences are the ones the import
// route takes, for example {"quantizations":"q4_k_m"}. An error from the
// registry (no importer matched, or an ambiguous modality) is returned as is.
func PreviewImport(uri string, details *hfapi.ModelDetails, preferences json.RawMessage) (ImportPreview, error) {
	cfg, matched, err := matchImporters(Details{HuggingFace: details, URI: uri, Preferences: preferences})
	if err != nil {
		return ImportPreview{}, err
	}
	out := ImportPreview{Importer: matched, Name: cfg.Name}
	var mc config.ModelConfig
	if yaml.Unmarshal([]byte(cfg.ConfigFile), &mc) == nil {
		out.Backend = mc.Backend
	}
	if out.Backend == "" {
		out.Backend = matched
	}
	for _, f := range cfg.Files {
		name := f.Filename
		if name == "" {
			name = filepath.Base(f.URI)
		}
		out.Files = append(out.Files, name)
	}
	return out, nil
}

// QuantMatches reports whether a quantization preference selects a GGUF file
// group named base. It is the rule the llama.cpp importer applies to
// preferences.quantizations: a whole-token, case-insensitive match, so "f16"
// does not select a "bf16" file.
func QuantMatches(base, preference string) bool {
	return quantTokenMatches(strings.ToLower(base), strings.ToLower(preference))
}
