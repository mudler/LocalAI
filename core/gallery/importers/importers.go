package importers

import (
	"encoding/json"

	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/gallery"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

var DefaultImporters = []Importer{
	&LlamaCPPImporter{},
	&MLXImporter{},
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

func DiscoverModelConfig(uri string, preferences json.RawMessage) (gallery.ModelConfig, error) {
	var err error
	var modelConfig gallery.ModelConfig

	hf := hfapi.NewClient()

	hfDetails, err := hf.GetModelDetails(uri)
	if err != nil {
		// maybe not a HF repository
		// TODO: maybe we can check if the URI is a valid HF repository
		log.Debug().Str("uri", uri).Msg("Failed to get model details, maybe not a HF repository")
	}

	details := Details{
		HuggingFace: hfDetails,
		URI:         uri,
		Preferences: preferences,
	}

	for _, importer := range DefaultImporters {
		if importer.Match(details) {
			modelConfig, err = importer.Import(details)
			if err != nil {
				continue
			}
			break
		}
	}
	return modelConfig, err
}
