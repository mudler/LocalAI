package importers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

// ErrAmbiguousImport is returned when HuggingFace metadata hints at a known
// modality (e.g. pipeline_tag: "automatic-speech-recognition") but no
// importer's artefact-level detection matches the repository. Callers should
// pass preferences.backend to disambiguate. Use errors.Is to match regardless
// of wrapping — DiscoverModelConfig wraps with fmt.Errorf("%w: ...") so the
// full context reaches HTTP consumers without losing sentinel identity.
var ErrAmbiguousImport = errors.New("importer: ambiguous — specify preferences.backend")

// ambiguousModalities enumerates the HF pipeline_tag values that are narrow
// enough to be confident we should surface ambiguity instead of a generic
// "no importer matched" error. Tags outside this whitelist keep the previous
// behaviour (plain error) so we don't block uncommon-but-still-valid imports.
var ambiguousModalities = map[string]struct{}{
	"automatic-speech-recognition": {},
	"text-to-speech":               {},
	"sentence-similarity":          {},
	"text-classification":          {},
	"object-detection":             {},
	"text-to-image":                {},
}

var defaultImporters = []Importer{
	// ASR (Batch 1)
	&WhisperImporter{},
	&MoonshineImporter{},
	&NemoImporter{},
	&FasterWhisperImporter{},
	&QwenASRImporter{},
	// TTS (Batch 2)
	&PiperImporter{},
	&BarkImporter{},
	&FishSpeechImporter{},
	&OutettsImporter{},
	&VoxCPMImporter{},
	&KokoroImporter{},
	&KittenTTSImporter{},
	&NeuTTSImporter{},
	&ChatterboxImporter{},
	&VibeVoiceImporter{},
	&CoquiImporter{},
	// Image/Video (Batch 3)
	&StableDiffusionGGMLImporter{},
	&ACEStepImporter{},
	// Existing
	&LlamaCPPImporter{},
	&MLXImporter{},
	&VLLMImporter{},
	&TransformersImporter{},
	&DiffuserImporter{},
}

type Details struct {
	HuggingFace *hfapi.ModelDetails
	URI         string
	Preferences json.RawMessage
}

type Importer interface {
	Match(details Details) bool
	Import(details Details) (gallery.ModelConfig, error)
	// Name is the canonical backend name (e.g. "llama-cpp"). Used by
	// /backends/known to populate the import form dropdown.
	Name() string
	// Modality is the backend's primary modality ("text", "asr", "tts",
	// "image", "embeddings", "reranker", "detection", "vad"). Used for
	// grouping in the UI.
	Modality() string
	// AutoDetects is true when Match() can fire without an explicit
	// preferences.backend. Preference-only entries surface as
	// AutoDetect=false in /backends/known.
	AutoDetects() bool
}

// KnownBackendEntry describes one backend advertised by an importer.
// Importers that host drop-in replacements (e.g. llama-cpp hosting
// ik-llama-cpp and turboquant) return additional entries via
// AdditionalBackendsProvider so the endpoint can surface them without
// registering separate importers.
type KnownBackendEntry struct {
	Name        string
	Modality    string
	Description string
}

// AdditionalBackendsProvider is implemented by importers that advertise
// drop-in replacements sharing their Match/Import logic. The entries
// appear in /backends/known with AutoDetect=false since they are
// preference-only.
type AdditionalBackendsProvider interface {
	AdditionalBackends() []KnownBackendEntry
}

// Registry returns the list of registered importers. Callers must not
// mutate the returned slice.
func Registry() []Importer {
	return defaultImporters
}

func hasYAMLExtension(uri string) bool {
	return strings.HasSuffix(uri, ".yaml") || strings.HasSuffix(uri, ".yml")
}

func DiscoverModelConfig(uri string, preferences json.RawMessage) (gallery.ModelConfig, error) {
	var err error
	var modelConfig gallery.ModelConfig

	hf := hfapi.NewClient()

	hfrepoID := strings.ReplaceAll(uri, "huggingface://", "")
	hfrepoID = strings.ReplaceAll(hfrepoID, "hf://", "")
	hfrepoID = strings.ReplaceAll(hfrepoID, "https://huggingface.co/", "")

	hfDetails, err := hf.GetModelDetails(hfrepoID)
	if err != nil {
		// maybe not a HF repository
		// TODO: maybe we can check if the URI is a valid HF repository
		xlog.Debug("Failed to get model details, maybe not a HF repository", "uri", uri, "hfrepoID", hfrepoID)
	} else {
		xlog.Debug("Got model details", "uri", uri)
		xlog.Debug("Model details", "details", hfDetails)
	}

	// handle local config files ("/my-model.yaml" or "file://my-model.yaml")
	localURI := uri
	if strings.HasPrefix(uri, downloader.LocalPrefix) {
		localURI = strings.TrimPrefix(uri, downloader.LocalPrefix)
	}

	// if a file exists or it's an url that ends with .yaml or .yml, read the config file directly
	if _, e := os.Stat(localURI); hasYAMLExtension(localURI) && (e == nil || downloader.URI(localURI).LooksLikeURL()) {
		var modelYAML []byte
		if downloader.URI(localURI).LooksLikeURL() {
			err := downloader.URI(localURI).ReadWithCallback(localURI, func(url string, i []byte) error {
				modelYAML = i
				return nil
			})
			if err != nil {
				xlog.Error("error reading model definition", "error", err, "filepath", localURI)
				return gallery.ModelConfig{}, err
			}
		} else {
			modelYAML, err = os.ReadFile(localURI)
			if err != nil {
				xlog.Error("error reading model definition", "error", err, "filepath", localURI)
				return gallery.ModelConfig{}, err
			}
		}

		var modelConfig config.ModelConfig
		if e := yaml.Unmarshal(modelYAML, &modelConfig); e != nil {
			return gallery.ModelConfig{}, e
		}

		configFile, err := yaml.Marshal(modelConfig)
		return gallery.ModelConfig{
			Description: modelConfig.Description,
			Name:        modelConfig.Name,
			ConfigFile:  string(configFile),
		}, err
	}

	details := Details{
		HuggingFace: hfDetails,
		URI:         uri,
		Preferences: preferences,
	}

	importerMatched := false
	for _, importer := range defaultImporters {
		if importer.Match(details) {
			importerMatched = true
			modelConfig, err = importer.Import(details)
			if err != nil {
				continue
			}
			break
		}
	}
	if !importerMatched {
		// When HuggingFace metadata hints at a known, narrow modality but no
		// importer matched the artefacts, surface an explicit ambiguity so the
		// caller knows to pass preferences.backend rather than silently guess.
		if hfDetails != nil {
			if _, known := ambiguousModalities[hfDetails.PipelineTag]; known && hfDetails.PipelineTag != "" {
				return gallery.ModelConfig{}, fmt.Errorf("%w: detected modality %q for %s", ErrAmbiguousImport, hfDetails.PipelineTag, uri)
			}
		}
		return gallery.ModelConfig{}, fmt.Errorf("no importer matched for %s", uri)
	}
	return modelConfig, nil
}
