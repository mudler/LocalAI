package importers

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

var defaultImporters = []Importer{
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
		log.Debug().Str("uri", uri).Str("hfrepoID", hfrepoID).Msg("Failed to get model details, maybe not a HF repository")
	} else {
		log.Debug().Str("uri", uri).Msg("Got model details")
		log.Debug().Any("details", hfDetails).Msg("Model details")
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
				log.Error().Err(err).Str("filepath", localURI).Msg("error reading model definition")
				return gallery.ModelConfig{}, err
			}
		} else {
			modelYAML, err = os.ReadFile(localURI)
			if err != nil {
				log.Error().Err(err).Str("filepath", localURI).Msg("error reading model definition")
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
		return gallery.ModelConfig{}, fmt.Errorf("no importer matched for %s", uri)
	}
	return modelConfig, nil
}
