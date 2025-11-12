package importers

import (
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
)

var DefaultImporters = []Importer{
	&LlamaCPPImporter{},
	&MLXImporter{},
}

type Importer interface {
	Match(uri string, request schema.ImportModelRequest) bool
	Import(uri string, request schema.ImportModelRequest) (gallery.ModelConfig, error)
}
