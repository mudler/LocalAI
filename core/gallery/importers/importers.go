package importers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
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
// of wrapping — DiscoverModelConfig returns a typed AmbiguousImportError that
// carries the detected modality + candidate backends, and whose Is() matches
// this sentinel so legacy callers keep working.
var ErrAmbiguousImport = errors.New("importer: ambiguous — specify preferences.backend")

// AmbiguousImportError is the concrete error DiscoverModelConfig returns when
// it can't pick an importer automatically. It carries the importer-modality
// key (e.g. "tts", "asr") and the list of candidate backend names so HTTP
// consumers can render a picker without re-deriving the mapping from HF
// pipeline_tag values.
type AmbiguousImportError struct {
	// Modality is the importer modality key ("text", "asr", "tts", "image",
	// "embeddings", "reranker", "detection"). Pre-mapped from the HF
	// pipeline_tag so the UI doesn't have to.
	Modality string
	// Candidates is the list of backend names whose Modality() matches — a
	// subset of the importer registry plus AdditionalBackendsProvider
	// drop-ins.
	Candidates []string
	// URI is the original URI that triggered the ambiguity.
	URI string
	// PipelineTag is the raw HF pipeline_tag value as reported by the model
	// metadata — preserved for logging / debugging.
	PipelineTag string
}

func (e *AmbiguousImportError) Error() string {
	return fmt.Sprintf("importer: ambiguous — detected modality %q (pipeline_tag=%q) for %s, candidates: %v",
		e.Modality, e.PipelineTag, e.URI, e.Candidates)
}

// Is lets callers match with errors.Is(err, ErrAmbiguousImport) without caring
// about the typed shape.
func (e *AmbiguousImportError) Is(target error) bool {
	return target == ErrAmbiguousImport
}

// ambiguousModalities enumerates the HF pipeline_tag values that are narrow
// enough to be confident we should surface ambiguity instead of a generic
// "no importer matched" error. Tags outside this whitelist keep the previous
// behaviour (plain error) so we don't block uncommon-but-still-valid imports.
// The mapped value is the importer modality key used to filter candidates.
var ambiguousModalities = map[string]string{
	"automatic-speech-recognition": "asr",
	"text-to-speech":               "tts",
	"sentence-similarity":          "embeddings",
	"text-classification":          "reranker",
	"object-detection":             "detection",
	"text-to-image":                "image",
}

// PipelineTagToModality maps HF pipeline_tag strings to the importer modality
// key used internally (and by /backends/known). Returns the modality + true
// when the tag is in the ambiguous whitelist; "" + false otherwise.
func PipelineTagToModality(pipelineTag string) (string, bool) {
	m, ok := ambiguousModalities[pipelineTag]
	return m, ok
}

// CandidatesForModality returns the backend names whose importer modality
// matches the requested key. Includes AdditionalBackendsProvider drop-ins so
// entries like ik-llama-cpp surface for text modalities. Results are sorted
// for deterministic ordering in API responses.
func CandidatesForModality(modality string) []string {
	seen := make(map[string]struct{})
	for _, imp := range defaultImporters {
		if imp.Modality() != modality {
			continue
		}
		seen[imp.Name()] = struct{}{}
		if host, ok := imp.(AdditionalBackendsProvider); ok {
			for _, extra := range host.AdditionalBackends() {
				if extra.Modality != modality {
					continue
				}
				seen[extra.Name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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
	// Text LLM (Batch 4) — VLLMOmniImporter must stay ahead of
	// VLLMImporter so Qwen Omni repos (which also carry tokenizer
	// files) route to vllm-omni rather than plain vllm.
	&VLLMOmniImporter{},
	// Embeddings / rerankers / detection / VAD (Batch 5)
	// SileroVADImporter first — unique filename signal, cannot collide.
	&SileroVADImporter{},
	// RerankersImporter must run before SentenceTransformers and
	// Transformers — some reranker repos ship modules.json and tokenizer
	// files that those importers would otherwise claim.
	&RerankersImporter{},
	// SentenceTransformersImporter must run before TransformersImporter:
	// sentence-transformers repos ship tokenizer.json which transformers
	// would otherwise claim.
	&SentenceTransformersImporter{},
	// RFDetrImporter must run before TransformersImporter — RF-DETR
	// checkpoints may carry tokenizer-adjacent artefacts.
	&RFDetrImporter{},
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
		if hfDetails != nil && hfDetails.PipelineTag != "" {
			if modality, known := ambiguousModalities[hfDetails.PipelineTag]; known {
				return gallery.ModelConfig{}, &AmbiguousImportError{
					Modality:    modality,
					Candidates:  CandidatesForModality(modality),
					URI:         uri,
					PipelineTag: hfDetails.PipelineTag,
				}
			}
		}
		return gallery.ModelConfig{}, fmt.Errorf("no importer matched for %s", uri)
	}
	return modelConfig, nil
}
