package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &SileroVADImporter{}

// SileroVADImporter recognises the Silero Voice Activity Detection models
// distributed as ONNX weights. The canonical packaging ships a file named
// exactly "silero_vad.onnx" (see snakers4/silero-vad); we additionally
// accept any ONNX file under the "snakers4" owner so community-mirrored
// copies still route here. preferences.backend="silero-vad" overrides
// detection.
type SileroVADImporter struct{}

func (i *SileroVADImporter) Name() string      { return "silero-vad" }
func (i *SileroVADImporter) Modality() string  { return "vad" }
func (i *SileroVADImporter) AutoDetects() bool { return true }

func (i *SileroVADImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		if err := json.Unmarshal(preferences, &preferencesMap); err != nil {
			return false
		}
	}

	if b, ok := preferencesMap["backend"].(string); ok && b == "silero-vad" {
		return true
	}

	if details.HuggingFace != nil {
		if HasFile(details.HuggingFace.Files, "silero_vad.onnx") {
			return true
		}
		if strings.EqualFold(details.HuggingFace.Author, "snakers4") && HasONNX(details.HuggingFace.Files) {
			return true
		}
	}

	// Fallback: hfapi recursion bug may leave HuggingFace nil — decide
	// from the URI owner/repo. The snakers4 organisation ships only
	// silero-* projects, so URI-level ownership is a safe signal.
	if owner, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		if strings.EqualFold(owner, "snakers4") && strings.Contains(strings.ToLower(repo), "silero") {
			return true
		}
	}

	return false
}

func (i *SileroVADImporter) Import(details Details) (gallery.ModelConfig, error) {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		if err := json.Unmarshal(preferences, &preferencesMap); err != nil {
			return gallery.ModelConfig{}, err
		}
	}

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = filepath.Base(details.URI)
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "Imported from " + details.URI
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	// Prefer the canonical silero_vad.onnx filename when available so the
	// emitted YAML points at the actual weights. Fall back to the HF repo
	// path otherwise — users can adjust after import.
	model := details.URI
	if details.HuggingFace != nil {
		for _, f := range details.HuggingFace.Files {
			if filepath.Base(f.Path) == "silero_vad.onnx" {
				cfg.Files = append(cfg.Files, gallery.File{
					URI:      f.URL,
					Filename: "silero_vad.onnx",
					SHA256:   f.SHA256,
				})
				model = "silero_vad.onnx"
				break
			}
		}
		if model == details.URI && details.HuggingFace.ModelID != "" {
			model = details.HuggingFace.ModelID
		}
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "silero-vad",
		KnownUsecaseStrings: []string{"vad"},
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: model},
		},
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	cfg.ConfigFile = string(data)

	return cfg, nil
}
