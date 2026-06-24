package importers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/xlog"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &VLLMImporter{}

type VLLMImporter struct{}

func (i *VLLMImporter) Name() string      { return "vllm" }
func (i *VLLMImporter) Modality() string  { return "text" }
func (i *VLLMImporter) AutoDetects() bool { return true }

// AdditionalBackends advertises vllm-cpp (the LocalAI-team C++ port of vLLM)
// as a preference-only drop-in: it consumes the same safetensors model dirs
// this importer detects, so selecting it swaps the emitted backend field while
// reusing the vllm Match/Import pipeline.
func (i *VLLMImporter) AdditionalBackends() []KnownBackendEntry {
	return []KnownBackendEntry{
		{Name: "vllm-cpp", Modality: "text", Description: "C++ port of vLLM by the LocalAI team (safetensors + GGUF, no Python at inference)"},
	}
}

func (i *VLLMImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	err = json.Unmarshal(preferences, &preferencesMap)
	if err != nil {
		return false
	}

	b, ok := preferencesMap["backend"].(string)
	if ok && (b == "vllm" || b == "vllm-cpp") {
		return true
	}

	if details.HuggingFace != nil {
		for _, file := range details.HuggingFace.Files {
			if strings.Contains(file.Path, "tokenizer.json") ||
				strings.Contains(file.Path, "tokenizer_config.json") {
				return true
			}
		}
	}

	return false
}

func (i *VLLMImporter) Import(details Details) (gallery.ModelConfig, error) {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	err = json.Unmarshal(preferences, &preferencesMap)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = filepath.Base(details.URI)
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "Imported from " + details.URI
	}

	backend := "vllm"
	b, ok := preferencesMap["backend"].(string)
	if ok {
		backend = b
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		KnownUsecaseStrings: []string{config.UsecaseChat},
		Backend:             backend,
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: LocalModelPath(details.URI),
			},
		},
		TemplateConfig: config.TemplateConfig{
			UseTokenizerTemplate: true,
		},
	}

	// Apply per-model-family inference parameter defaults
	config.ApplyInferenceDefaults(&modelConfig, details.URI)

	if backend == "vllm-cpp" {
		// vllm-cpp applies the model's chat template ENGINE-side (like the
		// vllm python backend, so use_tokenizer_template carries over), but
		// tool/reasoning parsing is the engine's own autoparser pipeline -
		// the vllm-python tool_parser/reasoning_parser options don't apply.
		//
		// Auto-detect a Multi-Token Prediction head, the safetensors analogue
		// of the llama-cpp importer's GGUF hook, so a freshly imported
		// Qwen3.5 / Qwen3.6 config already carries speculative decoding in its
		// engine_args instead of leaving the throughput on the table.
		maybeApplyVLLMSpeculativeDefaults(&modelConfig, details)
	} else {
		// Auto-detect tool_parser and reasoning_parser for known model families.
		// Surfacing them in the generated YAML lets users see and edit the choices.
		parsers := config.MatchParserDefaults(details.URI)
		if parsers != nil {
			if tp, ok := parsers["tool_parser"]; ok {
				modelConfig.Options = append(modelConfig.Options, "tool_parser:"+tp)
			}
			if rp, ok := parsers["reasoning_parser"]; ok {
				modelConfig.Options = append(modelConfig.Options, "reasoning_parser:"+rp)
			}
		}
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

// maxSpecConfigProbeBytes caps the config.json body we read. Real ones are a
// few KB; the cap keeps a hostile or mislabelled URL from streaming into the
// importer.
const maxSpecConfigProbeBytes = 1 << 20 // 1 MiB

// specConfigProbeTimeout bounds the config.json fetch. Detection is an
// optimisation, so it must never hold an import open for long.
const specConfigProbeTimeout = 30 * time.Second

// specConfigFetcher is the seam the config.json probe goes through, so tests can
// drive the whole import path without a network round trip.
var specConfigFetcher = fetchProbeBody

// maybeApplyVLLMSpeculativeDefaults fetches the repository's config.json and,
// when it declares a Multi-Token Prediction head, enables MTP speculative
// decoding in the emitted engine_args. This is the safetensors counterpart of
// the llama-cpp importer's GGUF header probe.
//
// Every failure is non-fatal and logged at debug: a network blip, a private
// repo, or a config.json this doesn't understand must leave the import working
// exactly as it did before, just without the speculative default.
func maybeApplyVLLMSpeculativeDefaults(modelConfig *config.ModelConfig, details Details) {
	probeURL := vllmSpecProbeURL(details)
	if probeURL == "" {
		return
	}

	body, err := specConfigFetcher(probeURL)
	if err != nil {
		xlog.Debug("[vllm-spec-importer] could not read config.json for MTP detection", "uri", probeURL, "error", err)
		return
	}

	applySpecFromConfigJSON(modelConfig, body, details.URI)
}

// applySpecFromConfigJSON is the decision half of the probe, split out so it can
// be exercised without a network round trip.
func applySpecFromConfigJSON(modelConfig *config.ModelConfig, body []byte, uri string) {
	if config.IsDFlashDraftConfig(body) {
		// A DFlash draft cannot serve on its own - it only proposes tokens for
		// a target model to verify. Say so rather than emitting a config that
		// would fail at load.
		xlog.Warn("[vllm-spec-importer] this repository is a DFlash DRAFT checkpoint, not a servable model; "+
			"import the TARGET model and point engine_args.speculative_config at this repo "+
			`({"method":"dflash","model":"<this repo>"})`, "uri", uri)
		return
	}

	n, ok := config.HasSafetensorsMTPHead(body)
	if !ok {
		return
	}
	config.ApplyVLLMSpeculativeDefaults(modelConfig, n)
}

// vllmSpecProbeURL returns the HTTP(S) URL of the repository's config.json, or
// "" when the import isn't backed by a HuggingFace repo we can fetch from (a
// local directory import, an OCI artifact, ...).
func vllmSpecProbeURL(details Details) string {
	if details.HuggingFace == nil || details.HuggingFace.ModelID == "" {
		return ""
	}
	return resolveHTTPProbe(downloader.HuggingFacePrefix + details.HuggingFace.ModelID + "/config.json")
}

// fetchProbeBody GETs a small remote JSON document under a short timeout.
func fetchProbeBody(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), specConfigProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpclient.NewWithTimeout(specConfigProbeTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxSpecConfigProbeBytes))
}
