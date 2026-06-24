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

var _ Importer = &LiquidAudioImporter{}

// LiquidAudioImporter recognises LiquidAI's LFM2-Audio family (LFM2-Audio-1.5B,
// LFM2.5-Audio-1.5B, community finetunes) and routes them to the Python
// `liquid-audio` backend. Detection is by repo-name substring so third-party
// mirrors still match. preferences.backend="liquid-audio" overrides detection.
//
// Once upstream llama.cpp PR #18641 lands and the GGUF gallery entries are
// added, GGUF mirrors of these models should route to llama-cpp; that's
// handled by ordering LlamaCPPImporter after this one and by the explicit
// "-gguf" exclusion below.
type LiquidAudioImporter struct{}

func (i *LiquidAudioImporter) Name() string      { return "liquid-audio" }
func (i *LiquidAudioImporter) Modality() string  { return "tts" }
func (i *LiquidAudioImporter) AutoDetects() bool { return true }

func (i *LiquidAudioImporter) Match(details Details) bool {
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

	if b, ok := preferencesMap["backend"].(string); ok && b == "liquid-audio" {
		return true
	}

	matchRepo := func(repo string) bool {
		r := strings.ToLower(repo)
		// Cede GGUF mirrors to the (later-ordered) llama-cpp importer.
		if strings.HasSuffix(r, "-gguf") {
			return false
		}
		return strings.Contains(r, "lfm2-audio") || strings.Contains(r, "lfm2.5-audio")
	}

	if details.HuggingFace != nil {
		repoName := details.HuggingFace.ModelID
		if idx := strings.Index(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		if matchRepo(repoName) {
			return true
		}
	}

	if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		return matchRepo(repo)
	}
	return false
}

func (i *LiquidAudioImporter) Import(details Details) (gallery.ModelConfig, error) {
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

	model := details.URI
	if details.HuggingFace != nil && details.HuggingFace.ModelID != "" {
		model = details.HuggingFace.ModelID
	}

	// Preferences may pin the mode (chat / asr / tts / s2s / finetune).
	// Default to s2s — the headline any-to-any use case.
	mode, _ := preferencesMap["mode"].(string)
	if mode == "" {
		mode = "s2s"
	}

	options := []string{"mode:" + mode}
	if voice, ok := preferencesMap["voice"].(string); ok && voice != "" {
		options = append(options, "voice:"+voice)
	}

	usecases := []string{"chat"}
	switch mode {
	case "asr":
		usecases = []string{"transcript"}
	case "tts":
		usecases = []string{"tts"}
	case "s2s":
		// realtime_audio surfaces the model on the Talk page; chat/tts/
		// transcript/vad keep the standalone OpenAI-compatible endpoints
		// working since liquid-audio implements all of them.
		usecases = []string{"realtime_audio", "chat", "tts", "transcript", "vad"}
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "liquid-audio",
		KnownUsecaseStrings: usecases,
		Options:             options,
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: model},
		},
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	return gallery.ModelConfig{
		Name:        name,
		Description: description,
		ConfigFile:  string(data),
	}, nil
}
