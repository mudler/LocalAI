package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &VibeVoiceCppImporter{}

// VibeVoiceCppImporter recognises the GGUF bundle that the vibevoice.cpp
// backend consumes — primary model file (vibevoice-realtime-*.gguf for TTS or
// vibevoice-asr-*.gguf for ASR), a sibling tokenizer.gguf (always required),
// and optional voice-*.gguf prompts for TTS voice cloning. Detection fires on
// the HF repo name containing "vibevoice.cpp"/"vibevoice-cpp", or on the
// presence of a vibevoice-*.gguf + tokenizer.gguf pair. preferences.backend
// ="vibevoice-cpp" forces the importer regardless of artefacts.
//
// Role pick: defaults to TTS (the realtime model is small and the common
// case). preferences.usecase="asr" routes to the ASR/diarization model. If a
// repo only ships one of the two roles, that role wins automatically.
//
// MUST be registered ahead of VibeVoiceImporter — the older Python-backed
// importer matches any repo with "vibevoice" in the name, which would
// otherwise swallow the C++ bundle.
type VibeVoiceCppImporter struct{}

func (i *VibeVoiceCppImporter) Name() string      { return "vibevoice-cpp" }
func (i *VibeVoiceCppImporter) Modality() string  { return "tts" }
func (i *VibeVoiceCppImporter) AutoDetects() bool { return true }

func (i *VibeVoiceCppImporter) Match(details Details) bool {
	preferencesMap := unmarshalPreferences(details.Preferences)
	if b, ok := preferencesMap["backend"].(string); ok && b == "vibevoice-cpp" {
		return true
	}

	// Repo-name signal: anything carrying "vibevoice.cpp" or "vibevoice-cpp"
	// — the canonical naming for the C++ port bundles.
	repoSignals := []string{strings.ToLower(repoNameOnly(details))}
	if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok {
		repoSignals = append(repoSignals, strings.ToLower(repo))
	}
	for _, s := range repoSignals {
		if strings.Contains(s, "vibevoice.cpp") || strings.Contains(s, "vibevoice-cpp") {
			return true
		}
	}

	// File-listing signal: a vibevoice-*.gguf primary + tokenizer.gguf is
	// only what the C++ backend ships — the Python VibeVoice fork distributes
	// safetensors, never GGUF.
	if details.HuggingFace != nil &&
		HasFile(details.HuggingFace.Files, "tokenizer.gguf") &&
		hasVibeVoiceGGUF(details.HuggingFace.Files) {
		return true
	}

	return false
}

func (i *VibeVoiceCppImporter) Import(details Details) (gallery.ModelConfig, error) {
	preferencesMap := unmarshalPreferences(details.Preferences)

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = filepath.Base(details.URI)
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "Imported from " + details.URI
	}

	// Quant preference — default order matches what mudler/vibevoice.cpp-models
	// ships today. Same comma-separated convention as whisper / llama-cpp.
	quants := []string{"q8_0", "q4_k", "q5_k", "q4_0"}
	if preferred, ok := preferencesMap["quantizations"].(string); ok && preferred != "" {
		quants = strings.Split(preferred, ",")
	}

	usecase := strings.ToLower(stringPref(preferencesMap, "usecase"))

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	modelConfig := config.ModelConfig{
		Name:        name,
		Description: description,
		Backend:     "vibevoice-cpp",
	}

	// Without HF metadata we can only emit a skeleton config — the user must
	// edit it post-import to point at real files. Mirrors whisper's bare-URI
	// fallback so preference-only invocations still produce something usable.
	if details.HuggingFace == nil {
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: filepath.Base(details.URI)},
		}
		if usecase == "asr" {
			modelConfig.KnownUsecaseStrings = []string{"transcript"}
			modelConfig.Options = []string{"type=asr", "tokenizer=tokenizer.gguf"}
		} else {
			modelConfig.KnownUsecaseStrings = []string{"tts"}
			modelConfig.Options = []string{"tokenizer=tokenizer.gguf"}
		}
		data, err := yaml.Marshal(modelConfig)
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		cfg.ConfigFile = string(data)
		return cfg, nil
	}

	files := details.HuggingFace.Files
	ttsFiles := filterByPrefix(files, "vibevoice-realtime-")
	asrFiles := filterByPrefix(files, "vibevoice-asr-")

	// Auto-pick role when the repo only ships one. Explicit usecase wins.
	role := usecase
	if role == "" {
		switch {
		case len(ttsFiles) > 0 && len(asrFiles) == 0:
			role = "tts"
		case len(asrFiles) > 0 && len(ttsFiles) == 0:
			role = "asr"
		default:
			role = "tts" // default: realtime TTS is the smaller, more common case
		}
	}

	// Layout under <models>/vibevoice-cpp/<name>/ — same pattern as whisper's
	// nesting so multiple imports of the same upstream repo (with different
	// quants) don't collide on disk. Options[] paths are emitted relative to
	// opts.ModelPath, which the backend resolves against the LocalAI models
	// root in govibevoicecpp.go:resolvePath.
	relDir := filepath.Join("vibevoice-cpp", name)

	var primary []hfapi.ModelFile
	switch role {
	case "asr", "transcript", "stt", "speech-to-text":
		primary = asrFiles
		modelConfig.KnownUsecaseStrings = []string{"transcript"}
	default:
		primary = ttsFiles
		modelConfig.KnownUsecaseStrings = []string{"tts"}
	}
	// If the requested role has no matching files, fall back to any
	// vibevoice-*.gguf so the import still produces something runnable.
	if len(primary) == 0 {
		primary = filterByPrefix(files, "vibevoice-")
	}

	chosen, ok := pickPreferredGGUFFile(primary, quants)
	if !ok {
		// Nothing to download. Emit the skeleton — same shape as the
		// no-HF-metadata branch above, just with a sensible default name.
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: name + ".gguf"},
		}
		if role == "asr" {
			modelConfig.Options = []string{"type=asr", "tokenizer=" + filepath.Join(relDir, "tokenizer.gguf")}
		} else {
			modelConfig.Options = []string{"tokenizer=" + filepath.Join(relDir, "tokenizer.gguf")}
		}
		data, err := yaml.Marshal(modelConfig)
		if err != nil {
			return gallery.ModelConfig{}, err
		}
		cfg.ConfigFile = string(data)
		return cfg, nil
	}

	modelTarget := filepath.Join(relDir, filepath.Base(chosen.Path))
	cfg.Files = append(cfg.Files, gallery.File{
		URI:      chosen.URL,
		Filename: modelTarget,
		SHA256:   chosen.SHA256,
	})
	modelConfig.PredictionOptions = schema.PredictionOptions{
		BasicModelRequest: schema.BasicModelRequest{Model: modelTarget},
	}

	// tokenizer.gguf is mandatory — Load() rejects without it. Always pull
	// it when the repo provides one (every official vibevoice.cpp bundle does).
	options := []string{}
	if role == "asr" {
		options = append(options, "type=asr")
	}
	if tok, ok := findFile(files, "tokenizer.gguf"); ok {
		tokTarget := filepath.Join(relDir, "tokenizer.gguf")
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      tok.URL,
			Filename: tokTarget,
			SHA256:   tok.SHA256,
		})
		options = append(options, "tokenizer="+tokTarget)
	}

	// For TTS, ship the first voice-*.gguf as a default — the backend needs
	// a reference voice to clone from. ASR doesn't use voice prompts.
	if role != "asr" {
		if voice, ok := pickVoicePrompt(files, stringPref(preferencesMap, "voice")); ok {
			voiceTarget := filepath.Join(relDir, filepath.Base(voice.Path))
			cfg.Files = append(cfg.Files, gallery.File{
				URI:      voice.URL,
				Filename: voiceTarget,
				SHA256:   voice.SHA256,
			})
			options = append(options, "voice="+voiceTarget)
		}
	}
	modelConfig.Options = options

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	cfg.ConfigFile = string(data)
	return cfg, nil
}

// hasVibeVoiceGGUF returns true when any file matches "vibevoice-*.gguf"
// (case-insensitive). Narrow on purpose — third-party GGUF mirrors that
// re-pack the model under different filenames will be missed, but those
// users can pass preferences.backend="vibevoice-cpp" to force the importer.
func hasVibeVoiceGGUF(files []hfapi.ModelFile) bool {
	for _, f := range files {
		name := strings.ToLower(filepath.Base(f.Path))
		if strings.HasPrefix(name, "vibevoice-") && strings.HasSuffix(name, ".gguf") {
			return true
		}
	}
	return false
}

// filterByPrefix returns every file whose basename starts with prefix and
// ends in .gguf (case-insensitive on the suffix, exact on the prefix).
func filterByPrefix(files []hfapi.ModelFile, prefix string) []hfapi.ModelFile {
	var out []hfapi.ModelFile
	for _, f := range files {
		base := filepath.Base(f.Path)
		if !strings.HasPrefix(base, prefix) {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(base), ".gguf") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// findFile is HasFile's lookup-returning sibling. Returns the first file
// whose basename equals name (exact match), or false when none exists.
func findFile(files []hfapi.ModelFile, name string) (hfapi.ModelFile, bool) {
	for _, f := range files {
		if filepath.Base(f.Path) == name {
			return f, true
		}
	}
	return hfapi.ModelFile{}, false
}

// pickPreferredGGUFFile mirrors pickPreferredGGMLFile but operates on .gguf
// files: walks prefs in order, returns the first file whose basename contains
// any preference token (case-insensitive). On no match, falls back to the
// last file so a missing quant still yields a runnable import.
func pickPreferredGGUFFile(files []hfapi.ModelFile, prefs []string) (hfapi.ModelFile, bool) {
	if len(files) == 0 {
		return hfapi.ModelFile{}, false
	}
	for _, pref := range prefs {
		lower := strings.ToLower(strings.TrimSpace(pref))
		if lower == "" {
			continue
		}
		for _, f := range files {
			if strings.Contains(strings.ToLower(filepath.Base(f.Path)), lower) {
				return f, true
			}
		}
	}
	return files[len(files)-1], true
}

// pickVoicePrompt selects a voice-*.gguf to bundle with a TTS import.
// Honours an explicit preferences.voice substring (e.g. "Emma" picks
// voice-en-Emma.gguf); otherwise returns the first voice file in listing
// order so the choice is stable across imports of the same repo.
func pickVoicePrompt(files []hfapi.ModelFile, hint string) (hfapi.ModelFile, bool) {
	hint = strings.ToLower(strings.TrimSpace(hint))
	var voices []hfapi.ModelFile
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f.Path))
		if strings.HasPrefix(base, "voice-") && strings.HasSuffix(base, ".gguf") {
			voices = append(voices, f)
		}
	}
	if len(voices) == 0 {
		return hfapi.ModelFile{}, false
	}
	if hint != "" {
		for _, v := range voices {
			if strings.Contains(strings.ToLower(filepath.Base(v.Path)), hint) {
				return v, true
			}
		}
	}
	return voices[0], true
}

// repoNameOnly extracts the repo basename (everything after the last "/")
// from HF metadata or, failing that, the URI. Empty when neither is set.
func repoNameOnly(details Details) string {
	if details.HuggingFace != nil {
		id := details.HuggingFace.ModelID
		if idx := strings.Index(id, "/"); idx >= 0 {
			return id[idx+1:]
		}
		return id
	}
	return ""
}

// unmarshalPreferences decodes details.Preferences into a generic map. Returns
// an empty map (never nil) on any failure so callers can index without nil
// checks. Bad JSON is silently ignored — every importer here treats
// preferences as best-effort hints.
func unmarshalPreferences(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	b, err := raw.MarshalJSON()
	if err != nil || len(b) == 0 {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

// stringPref reads a string preference by key, returning "" when missing or
// of the wrong type.
func stringPref(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
