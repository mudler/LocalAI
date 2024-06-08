package config

import (
	"path/filepath"

	"github.com/rs/zerolog/log"

	gguf "github.com/thxcode/gguf-parser-go"
)

type FamilyType uint8

const (
	Unknown FamilyType = iota
	LLaMa3             = iota
	LLama2             = iota
)

var defaultsTemplate map[FamilyType]TemplateConfig = map[FamilyType]TemplateConfig{
	LLaMa3: {},
}

func guessDefaultsFromFile(cfg *BackendConfig, modelPath string) {

	if modelPath == "" {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "modelPath is empty")
		return
	}

	// We try to guess only if we don't have a template defined already+
	f, err := gguf.ParseGGUFFile(filepath.Join(modelPath, cfg.ModelFileName()))
	if err != nil {
		// Only valid for gguf files
		log.Debug().Msgf("guessDefaultsFromFile: %s", "not a GGUF file")
		return
	}

	log.Debug().
		Any("eosTokenID", f.Tokenizer().EOSTokenID).
		Any("modelName", f.Model().Name).
		Any("architecture", f.Architecture().Architecture).Msgf("Model file loaded: %s", cfg.ModelFileName())

	if cfg.Name == "" {
		cfg.Name = f.Model().Name
	}

	if cfg.HasTemplate() {
		return
	}

	family := identifyFamily(f)

	if family == Unknown {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "family not identified")
		return
	}

	templ, ok := defaultsTemplate[family]
	if ok {
		cfg.TemplateConfig = templ
	}

}

func identifyFamily(f *gguf.GGUFFile) FamilyType {

	switch {
	case f.Model().Name == "llama":
		return LLaMa3
	}

	return Unknown
}
