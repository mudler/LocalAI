package embedded

import (
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/go-skynet/LocalAI/pkg/assets"
	"gopkg.in/yaml.v3"
)

var modelShorteners map[string]string

//go:embed model_library.yaml
var modelLibrary []byte

//go:embed models/*
var embeddedModels embed.FS

func ModelShortURL(s string) string {
	if _, ok := modelShorteners[s]; ok {
		s = modelShorteners[s]
	}

	return s
}

func init() {
	yaml.Unmarshal(modelLibrary, &modelShorteners)
}

// ExistsInModelsLibrary checks if a model exists in the embedded models library
func ExistsInModelsLibrary(s string) bool {
	f := fmt.Sprintf("%s.yaml", s)

	a := []string{}

	for _, j := range assets.ListFiles(embeddedModels) {
		a = append(a, strings.TrimPrefix(j, "models/"))
	}

	return slices.Contains(a, f)
}

// ResolveContent returns the content in the embedded model library
func ResolveContent(s string) ([]byte, error) {
	if ExistsInModelsLibrary(s) {
		return embeddedModels.ReadFile(fmt.Sprintf("models/%s.yaml", s))
	}

	return nil, fmt.Errorf("cannot find model %s", s)
}
