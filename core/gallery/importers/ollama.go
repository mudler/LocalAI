package importers

import (
	"math"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/xlog"
)

// ollamaLookupTimeout bounds the registry requests made to describe an Ollama
// model during an import: one manifest, the first bytes of the weights and the
// small params layer.
const ollamaLookupTimeout = 60 * time.Second

// ollamaImportNames are the names an Ollama import derives from the URI.
type ollamaImportNames struct {
	// display is "name:tag", as Ollama users write it.
	display string
	// model is the model name: "qwen3:8b" becomes "qwen3-8b".
	model string
	// modelFile and projectorFile always end in ".gguf".
	modelFile     string
	projectorFile string
}

// newOllamaImportNames derives the names of an ollama:// URI. The "library"
// namespace is dropped, a "user/model" namespace becomes "user-model", and an
// untagged model is "latest" (which adds nothing to the name).
func newOllamaImportNames(uri string) ollamaImportNames {
	ref := strings.TrimPrefix(uri, downloader.OllamaPrefix)
	ref, _, _ = strings.Cut(ref, "#")
	tag, repository, image := oci.ParseImageParts(ref)

	parts := []string{image}
	if repository != "library" {
		parts = []string{repository, image}
	}
	if tag != "latest" {
		parts = append(parts, tag)
	}
	model := safeFileName(strings.Join(parts, "-"))
	if model == "" {
		model = "ollama-model"
	}
	return ollamaImportNames{
		display:       oci.OllamaDisplayName(ref),
		model:         model,
		modelFile:     model + ".gguf",
		projectorFile: model + "-mmproj.gguf",
	}
}

// safeFileName keeps letters, digits, '.', '_' and '-' and turns every other
// character into '-', so the result is a plain file name on every system.
func safeFileName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
			}
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-.")
}

// samplingKeys names the keys of a map that carry the sampling settings. The
// Ollama params layer and the import preferences spell two of them differently.
type samplingKeys struct {
	maxTokens string
	stop      string
	// logUnknown logs the keys that are not sampling settings. It is off for
	// the preferences, whose other keys (name, backend) belong to the importer.
	logUnknown bool
}

var (
	ollamaSamplingKeys     = samplingKeys{maxTokens: "num_predict", stop: "stop", logUnknown: true}
	preferenceSamplingKeys = samplingKeys{maxTokens: "max_tokens", stop: "stopwords"}
)

// applySamplingDefaults copies the sampling settings found in values into the
// model config. It is defensive: a value of the wrong type or out of range is
// skipped, and a key that is not a sampling setting (num_ctx among them, which
// would pin a context size the host may not afford) is only logged.
func applySamplingDefaults(cfg *config.ModelConfig, values map[string]any, keys samplingKeys) {
	for key, raw := range values {
		switch key {
		case "temperature":
			if v, ok := number(raw); ok && v >= 0 {
				cfg.Temperature = &v
				continue
			}
		case "top_p":
			if v, ok := number(raw); ok && v >= 0 && v <= 1 {
				cfg.TopP = &v
				continue
			}
		case "min_p":
			if v, ok := number(raw); ok && v >= 0 && v <= 1 {
				cfg.MinP = &v
				continue
			}
		case "repeat_penalty":
			if v, ok := number(raw); ok && v > 0 {
				cfg.RepeatPenalty = v
				continue
			}
		case "top_k":
			if v, ok := integer(raw); ok && v > 0 {
				cfg.TopK = &v
				continue
			}
		case "seed":
			if v, ok := integer(raw); ok {
				cfg.Seed = &v
				continue
			}
		case keys.maxTokens:
			// Ollama writes -1 or 0 for "no limit", which leaves the default.
			if v, ok := integer(raw); ok && v > 0 {
				cfg.Maxtokens = &v
				continue
			}
		case keys.stop:
			if list, ok := stringList(raw); ok {
				cfg.StopWords = list
				continue
			}
		default:
			if keys.logUnknown {
				xlog.Debug("[ollama-importer] ignoring a setting", "key", key)
			}
			continue
		}
		xlog.Debug("[ollama-importer] ignoring a setting with an unusable value", "key", key)
	}
}

func number(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func integer(v any) (int, bool) {
	f, ok := number(v)
	if !ok || f != math.Trunc(f) || math.Abs(f) > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

// stringList returns the non-empty strings of a JSON array; the array is
// refused when it holds anything else or nothing usable.
func stringList(v any) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	var out []string
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, len(out) > 0
}
