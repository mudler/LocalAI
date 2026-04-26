package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/functions"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/xlog"
	"go.yaml.in/yaml/v2"
)

var (
	_ Importer                   = &LlamaCPPImporter{}
	_ AdditionalBackendsProvider = &LlamaCPPImporter{}
)

type LlamaCPPImporter struct{}

func (i *LlamaCPPImporter) Name() string     { return "llama-cpp" }
func (i *LlamaCPPImporter) Modality() string { return "text" }
func (i *LlamaCPPImporter) AutoDetects() bool { return true }

// AdditionalBackends advertises drop-in replacements that share the
// llama-cpp detection logic. They are preference-only: selecting one
// from the import form swaps the emitted YAML backend field but reuses
// the llama-cpp Match/Import pipeline.
func (i *LlamaCPPImporter) AdditionalBackends() []KnownBackendEntry {
	return []KnownBackendEntry{
		{Name: "ik-llama-cpp", Modality: "text", Description: "GGUF drop-in replacement for llama-cpp with ik-quants"},
		{Name: "turboquant", Modality: "text", Description: "GGUF drop-in replacement for llama-cpp with TurboQuant optimizations"},
	}
}

func (i *LlamaCPPImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		xlog.Error("failed to marshal preferences", "error", err)
		return false
	}

	preferencesMap := make(map[string]any)

	if len(preferences) > 0 {
		err = json.Unmarshal(preferences, &preferencesMap)
		if err != nil {
			xlog.Error("failed to unmarshal preferences", "error", err)
			return false
		}
	}

	uri := downloader.URI(details.URI)

	if preferencesMap["backend"] == "llama-cpp" {
		return true
	}

	if strings.HasSuffix(details.URI, ".gguf") {
		return true
	}

	if uri.LooksLikeOCI() {
		return true
	}

	if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			if strings.HasSuffix(file.Path, ".gguf") {
				return true
			}
		}
	}

	return false
}

func (i *LlamaCPPImporter) Import(details Details) (gallery.ModelConfig, error) {

	xlog.Debug("llama.cpp importer matched", "uri", details.URI)

	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		err = json.Unmarshal(preferences, &preferencesMap)
		if err != nil {
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

	preferedQuantizations, _ := preferencesMap["quantizations"].(string)
	quants := []string{"q4_k_m"}
	if preferedQuantizations != "" {
		quants = strings.Split(preferedQuantizations, ",")
	}

	mmprojQuants, _ := preferencesMap["mmproj_quantizations"].(string)
	mmprojQuantsList := []string{"fp16"}
	if mmprojQuants != "" {
		mmprojQuantsList = strings.Split(mmprojQuants, ",")
	}

	embeddings, _ := preferencesMap["embeddings"].(string)

	// Honour drop-in replacement preferences. Only the curated names
	// advertised via AdditionalBackends() are accepted; anything else
	// (including "llama-cpp" itself, or an unknown value) keeps the
	// default backend field so arbitrary input can't leak through. See
	// the AdditionalBackends method for the canonical list.
	backend := "llama-cpp"
	if b, ok := preferencesMap["backend"].(string); ok {
		switch b {
		case "ik-llama-cpp", "turboquant":
			backend = b
		}
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{"chat"},
		Options:             []string{"use_jinja:true"},
		Backend:             backend,
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
		FunctionsConfig: functions.FunctionsConfig{
			GrammarConfig: functions.GrammarConfig{
				NoGrammar: true,
			},
			AutomaticToolParsingFallback: true,
		},
	}

	if embeddings != "" && strings.ToLower(embeddings) == "true" || strings.ToLower(embeddings) == "yes" {
		trueV := true
		modelConfig.Embeddings = &trueV
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}

	uri := downloader.URI(details.URI)

	switch {
	case uri.LooksLikeOCI():
		ociName := strings.TrimPrefix(string(uri), downloader.OCIPrefix)
		ociName = strings.TrimPrefix(ociName, downloader.OllamaPrefix)
		ociName = strings.ReplaceAll(ociName, "/", "__")
		ociName = strings.ReplaceAll(ociName, ":", "__")
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: ociName,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: ociName,
			},
		}
	case uri.LooksLikeURL() && strings.HasSuffix(details.URI, ".gguf"):
		// Extract filename from URL
		fileName, e := uri.FilenameFromUrl()
		if e != nil {
			return gallery.ModelConfig{}, e
		}

		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: fileName,
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: fileName,
			},
		}
	case strings.HasSuffix(details.URI, ".gguf"):
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      details.URI,
			Filename: filepath.Base(details.URI),
		})
		modelConfig.PredictionOptions = schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: filepath.Base(details.URI),
			},
		}
	case details.HuggingFace != nil:
		// Split the repo listing into mmproj vs plain GGUF files, then group
		// shards so every multi-part GGUF (llama.cpp `-NNNNN-of-MMMMM.gguf`
		// pattern) is treated as one logical selection candidate. The
		// previous implementation picked files one at a time, so sharded
		// models ended up with only the last part referenced in the gallery
		// entry — useless to llama.cpp, which needs shard 1 and the whole
		// set to load a split model.
		var mmprojFiles, ggufFiles []hfapi.ModelFile
		for _, f := range details.HuggingFace.Files {
			lowerPath := strings.ToLower(f.Path)
			switch {
			case strings.Contains(lowerPath, "mmproj"):
				mmprojFiles = append(mmprojFiles, f)
			case strings.HasSuffix(lowerPath, ".gguf"):
				ggufFiles = append(ggufFiles, f)
			}
		}

		mmprojGroups := hfapi.GroupShards(mmprojFiles)
		ggufGroups := hfapi.GroupShards(ggufFiles)

		// Emit the model group first so cfg.Files[0] is the model — callers
		// and tests rely on the model file preceding any mmproj companion.
		if group := pickPreferredGroup(ggufGroups, quants); group != nil {
			appendShardGroup(&cfg, *group, filepath.Join("llama-cpp", "models", name))
		}
		if group := pickPreferredGroup(mmprojGroups, mmprojQuantsList); group != nil {
			appendShardGroup(&cfg, *group, filepath.Join("llama-cpp", "mmproj", name))
		}

		// Find first mmproj file and configure it in the config file
		for _, file := range cfg.Files {
			if !strings.Contains(strings.ToLower(file.Filename), "mmproj") {
				continue
			}
			modelConfig.MMProj = file.Filename
			break
		}

		// Find first non-mmproj file and configure it in the config file.
		// For sharded models this is shard 1 — llama.cpp's split loader
		// discovers the remaining shards by filename pattern from there.
		for _, file := range cfg.Files {
			if strings.Contains(strings.ToLower(file.Filename), "mmproj") {
				continue
			}
			modelConfig.PredictionOptions = schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{
					Model: file.Filename,
				},
			}
			break
		}
	}

	// Apply per-model-family inference parameter defaults
	config.ApplyInferenceDefaults(&modelConfig, details.URI)

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	cfg.ConfigFile = string(data)

	return cfg, nil
}

// pickPreferredGroup walks the preference list in priority order and returns
// the first group whose base filename contains any preference. When nothing
// matches, the last group wins — this preserves the historical "if the user
// asked for a quant we don't have, fall back to whatever's available"
// behaviour, lifted to whole shard sets.
func pickPreferredGroup(groups []hfapi.ShardGroup, prefs []string) *hfapi.ShardGroup {
	if len(groups) == 0 {
		return nil
	}
	for _, pref := range prefs {
		lower := strings.ToLower(pref)
		for i := range groups {
			if strings.Contains(strings.ToLower(groups[i].Base), lower) {
				return &groups[i]
			}
		}
	}
	return &groups[len(groups)-1]
}

// appendShardGroup copies every shard of group into cfg.Files under dest,
// skipping any entry whose target filename is already present so repeated
// calls (e.g. the rare case of mmproj + model picking the same group)
// don't produce duplicates.
func appendShardGroup(cfg *gallery.ModelConfig, group hfapi.ShardGroup, dest string) {
	for _, f := range group.Files {
		target := filepath.Join(dest, filepath.Base(f.Path))
		duplicate := false
		for _, existing := range cfg.Files {
			if existing.Filename == target {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      f.URL,
			Filename: target,
			SHA256:   f.SHA256,
		})
	}
}
