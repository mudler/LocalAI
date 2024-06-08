package config

import (
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"

	gguf "github.com/thxcode/gguf-parser-go"
)

type familyType uint8

const (
	Unknown familyType = iota
	LLaMa3             = iota
	LLama2             = iota
)

var defaultsTemplate map[familyType]TemplateConfig = map[familyType]TemplateConfig{
	LLaMa3: {
		Chat:        "<|begin_of_text|>{{.Input }}\n<|start_header_id|>assistant<|end_header_id|>",
		ChatMessage: "<|start_header_id|>{{ .RoleName }}<|end_header_id|>\n\n{{.Content }}<|eot_id|>",
	},
}

func guessDefaultsFromFile(cfg *BackendConfig, modelPath string) {

	if os.Getenv("LOCALAI_DISABLE_GUESSING") == "true" {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "guessing disabled with LOCALAI_DISABLE_GUESSING")
		return
	}

	if modelPath == "" {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "modelPath is empty")
		return
	}

	if cfg.HasTemplate() {
		// nothing to guess here
		log.Debug().Any("name", cfg.Name).Msgf("guessDefaultsFromFile: %s", "template already set")
		return
	}

	// We try to guess only if we don't have a template defined already
	f, err := gguf.ParseGGUFFile(filepath.Join(modelPath, cfg.ModelFileName()))
	if err != nil {
		// Only valid for gguf files
		log.Debug().Msgf("guessDefaultsFromFile: %s", "not a GGUF file")
		return
	}

	log.Debug().
		Any("eosTokenID", f.Tokenizer().EOSTokenID).
		Any("bosTokenID", f.Tokenizer().BOSTokenID).
		Any("modelName", f.Model().Name).
		Any("architecture", f.Architecture().Architecture).Msgf("Model file loaded: %s", cfg.ModelFileName())

	// guess the name
	if cfg.Name == "" {
		cfg.Name = f.Model().Name
	}

	family := identifyFamily(f)

	if family == Unknown {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "family not identified")
		return
	}

	templ, ok := defaultsTemplate[family]
	if ok {
		cfg.TemplateConfig = templ
		log.Debug().Any("family", family).Msgf("guessDefaultsFromFile: guessed template %+v", cfg.TemplateConfig)
	} else {
		log.Debug().Any("family", family).Msgf("guessDefaultsFromFile: no template found for family")
	}
}

func identifyFamily(f *gguf.GGUFFile) familyType {
	switch {
	case f.Architecture().Architecture == "llama" && f.Tokenizer().EOSTokenID == 128009:
		return LLaMa3
	}

	return Unknown
}
