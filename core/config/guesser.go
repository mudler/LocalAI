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
	LLaMa3
	LLama2
	CommandR
)

type settingsConfig struct {
	StopWords      []string
	TemplateConfig TemplateConfig
}

var defaultsSettings map[familyType]settingsConfig = map[familyType]settingsConfig{
	LLaMa3: {
		StopWords: []string{"<|eot_id|>"},
		TemplateConfig: TemplateConfig{
			Chat:        "<|begin_of_text|>{{.Input }}\n<|start_header_id|>assistant<|end_header_id|>",
			ChatMessage: "<|start_header_id|>{{ .RoleName }}<|end_header_id|>\n\n{{.Content }}<|eot_id|>",
		},
	},
	CommandR: {
		TemplateConfig: TemplateConfig{
			Chat: "{{.Input -}}<|START_OF_TURN_TOKEN|><|CHATBOT_TOKEN|>",
			Functions: `<|START_OF_TURN_TOKEN|><|SYSTEM_TOKEN|>
You are a function calling AI model, you can call the following functions:
## Available Tools
{{range .Functions}}
- {"type": "function", "function": {"name": "{{.Name}}", "description": "{{.Description}}", "parameters": {{toJson .Parameters}} }}
{{end}}
When using a tool, reply with JSON, for instance {"name": "tool_name", "arguments": {"param1": "value1", "param2": "value2"}}
<|END_OF_TURN_TOKEN|><|START_OF_TURN_TOKEN|><|CHATBOT_TOKEN|>{{.Input -}}`,
			ChatMessage: `{{if eq .RoleName "user" -}}
<|START_OF_TURN_TOKEN|><|USER_TOKEN|>{{.Content}}<|END_OF_TURN_TOKEN|>
{{- else if eq .RoleName "system" -}}
<|START_OF_TURN_TOKEN|><|SYSTEM_TOKEN|>{{.Content}}<|END_OF_TURN_TOKEN|>
{{- else if eq .RoleName "assistant" -}}
<|START_OF_TURN_TOKEN|><|CHATBOT_TOKEN|>{{.Content}}<|END_OF_TURN_TOKEN|>
{{- else if eq .RoleName "tool" -}}
<|START_OF_TURN_TOKEN|><|SYSTEM_TOKEN|>{{.Content}}<|END_OF_TURN_TOKEN|>
{{- else if .FunctionCall -}}
<|START_OF_TURN_TOKEN|><|CHATBOT_TOKEN|>{{toJson .FunctionCall}}}<|END_OF_TURN_TOKEN|>
{{- end -}}`,
		},
		StopWords: []string{"<|END_OF_TURN_TOKEN|>"},
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

	// identify template
	settings, ok := defaultsSettings[family]
	if ok {
		cfg.TemplateConfig = settings.TemplateConfig
		log.Debug().Any("family", family).Msgf("guessDefaultsFromFile: guessed template %+v", cfg.TemplateConfig)
		if len(cfg.StopWords) == 0 {
			cfg.StopWords = settings.StopWords
		}
	} else {
		log.Debug().Any("family", family).Msgf("guessDefaultsFromFile: no template found for family")
	}
}

func identifyFamily(f *gguf.GGUFFile) familyType {
	switch {
	case f.Architecture().Architecture == "llama" && f.Tokenizer().EOSTokenID == 128009:
		return LLaMa3
	case f.Architecture().Architecture == "command-r" && f.Tokenizer().EOSTokenID == 255001:
		return CommandR
	}

	return Unknown
}
