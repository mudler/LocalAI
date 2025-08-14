package config

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"

	gguf "github.com/gpustack/gguf-parser-go"
)

type familyType uint8

const (
	Unknown familyType = iota
	LLaMa3
	CommandR
	Phi3
	ChatML
	Mistral03
	Gemma
	DeepSeek2
)

const (
	defaultContextSize = 1024
	defaultNGPULayers  = 99999999
)

type settingsConfig struct {
	StopWords      []string
	TemplateConfig TemplateConfig
	RepeatPenalty  float64
}

// default settings to adopt with a given model family
var defaultsSettings map[familyType]settingsConfig = map[familyType]settingsConfig{
	Gemma: {
		RepeatPenalty: 1.0,
		StopWords:     []string{"<|im_end|>", "<end_of_turn>", "<start_of_turn>"},
		TemplateConfig: TemplateConfig{
			Chat:        "{{.Input }}\n<start_of_turn>model\n",
			ChatMessage: "<start_of_turn>{{if eq .RoleName \"assistant\" }}model{{else}}{{ .RoleName }}{{end}}\n{{ if .Content -}}\n{{.Content -}}\n{{ end -}}<end_of_turn>",
			Completion:  "{{.Input}}",
		},
	},
	DeepSeek2: {
		StopWords: []string{"<｜end▁of▁sentence｜>"},
		TemplateConfig: TemplateConfig{
			ChatMessage: `{{if eq .RoleName "user" -}}User: {{.Content }}
{{ end -}}
{{if eq .RoleName "assistant" -}}Assistant: {{.Content}}<｜end▁of▁sentence｜>{{end}}
{{if eq .RoleName "system" -}}{{.Content}}
{{end -}}`,
			Chat: "{{.Input -}}\nAssistant: ",
		},
	},
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
	Phi3: {
		TemplateConfig: TemplateConfig{
			Chat:        "{{.Input}}\n<|assistant|>",
			ChatMessage: "<|{{ .RoleName }}|>\n{{.Content}}<|end|>",
			Completion:  "{{.Input}}",
		},
		StopWords: []string{"<|end|>", "<|endoftext|>"},
	},
	ChatML: {
		TemplateConfig: TemplateConfig{
			Chat: "{{.Input -}}\n<|im_start|>assistant",
			Functions: `<|im_start|>system
You are a function calling AI model. You are provided with functions to execute. You may call one or more functions to assist with the user query. Don't make assumptions about what values to plug into functions. Here are the available tools:
{{range .Functions}}
{'type': 'function', 'function': {'name': '{{.Name}}', 'description': '{{.Description}}', 'parameters': {{toJson .Parameters}} }}
{{end}}
For each function call return a json object with function name and arguments
<|im_end|>
{{.Input -}}
<|im_start|>assistant`,
			ChatMessage: `<|im_start|>{{ .RoleName }}
{{ if .FunctionCall -}}
Function call:
{{ else if eq .RoleName "tool" -}}
Function response:
{{ end -}}
{{ if .Content -}}
{{.Content }}
{{ end -}}
{{ if .FunctionCall -}}
{{toJson .FunctionCall}}
{{ end -}}<|im_end|>`,
		},
		StopWords: []string{"<|im_end|>", "<dummy32000>", "</s>"},
	},
	Mistral03: {
		TemplateConfig: TemplateConfig{
			Chat:      "{{.Input -}}",
			Functions: `[AVAILABLE_TOOLS] [{{range .Functions}}{"type": "function", "function": {"name": "{{.Name}}", "description": "{{.Description}}", "parameters": {{toJson .Parameters}} }}{{end}} ] [/AVAILABLE_TOOLS]{{.Input }}`,
			ChatMessage: `{{if eq .RoleName "user" -}}
[INST] {{.Content }} [/INST]
{{- else if .FunctionCall -}}
[TOOL_CALLS] {{toJson .FunctionCall}} [/TOOL_CALLS]
{{- else if eq .RoleName "tool" -}}
[TOOL_RESULTS] {{.Content}} [/TOOL_RESULTS]
{{- else -}}
{{ .Content -}}
{{ end -}}`,
		},
		StopWords: []string{"<|im_end|>", "<dummy32000>", "</tool_call>", "<|eot_id|>", "<|end_of_text|>", "</s>", "[/TOOL_CALLS]", "[/ACTIONS]"},
	},
}

// this maps well known template used in HF to model families defined above
var knownTemplates = map[string]familyType{
	`{% if messages[0]['role'] == 'system' %}{% set system_message = messages[0]['content'] %}{% endif %}{% if system_message is defined %}{{ system_message }}{% endif %}{% for message in messages %}{% set content = message['content'] %}{% if message['role'] == 'user' %}{{ '<|im_start|>user\n' + content + '<|im_end|>\n<|im_start|>assistant\n' }}{% elif message['role'] == 'assistant' %}{{ content + '<|im_end|>' + '\n' }}{% endif %}{% endfor %}`:                              ChatML,
	`{{ bos_token }}{% for message in messages %}{% if (message['role'] == 'user') != (loop.index0 % 2 == 0) %}{{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}{% endif %}{% if message['role'] == 'user' %}{{ '[INST] ' + message['content'] + ' [/INST]' }}{% elif message['role'] == 'assistant' %}{{ message['content'] + eos_token}}{% else %}{{ raise_exception('Only user and assistant roles are supported!') }}{% endif %}{% endfor %}`: Mistral03,
}

func guessGGUFFromFile(cfg *ModelConfig, f *gguf.GGUFFile, defaultCtx int) {

	if defaultCtx == 0 && cfg.ContextSize == nil {
		ctxSize := f.EstimateLLaMACppRun().ContextSize
		if ctxSize > 0 {
			cSize := int(ctxSize)
			cfg.ContextSize = &cSize
		} else {
			defaultCtx = defaultContextSize
			cfg.ContextSize = &defaultCtx
		}
	}

	// GPU options
	if cfg.Options == nil {
		if xsysinfo.HasGPU("nvidia") || xsysinfo.HasGPU("amd") {
			cfg.Options = []string{"gpu"}
		}
	}

	// vram estimation
	vram, err := xsysinfo.TotalAvailableVRAM()
	if err != nil {
		log.Error().Msgf("guessDefaultsFromFile(TotalAvailableVRAM): %s", err)
	} else if vram > 0 {
		estimate, err := xsysinfo.EstimateGGUFVRAMUsage(f, vram)
		if err != nil {
			log.Error().Msgf("guessDefaultsFromFile(EstimateGGUFVRAMUsage): %s", err)
		} else {
			if estimate.IsFullOffload {
				log.Warn().Msgf("guessDefaultsFromFile: %s", "full offload is recommended")
			}

			if estimate.EstimatedVRAM > vram {
				log.Warn().Msgf("guessDefaultsFromFile: %s", "estimated VRAM usage is greater than available VRAM")
			}

			if cfg.NGPULayers == nil && estimate.EstimatedLayers > 0 {
				log.Debug().Msgf("guessDefaultsFromFile: %d layers estimated", estimate.EstimatedLayers)
				cfg.NGPULayers = &estimate.EstimatedLayers
			}
		}
	}

	if cfg.NGPULayers == nil {
		// we assume we want to offload all layers
		defaultHigh := defaultNGPULayers
		cfg.NGPULayers = &defaultHigh
	}

	log.Debug().Any("NGPULayers", cfg.NGPULayers).Msgf("guessDefaultsFromFile: %s", "NGPULayers set")

	// template estimations
	if cfg.HasTemplate() {
		// nothing to guess here
		log.Debug().Any("name", cfg.Name).Msgf("guessDefaultsFromFile: %s", "template already set")
		return
	}

	log.Debug().
		Any("eosTokenID", f.Tokenizer().EOSTokenID).
		Any("bosTokenID", f.Tokenizer().BOSTokenID).
		Any("modelName", f.Metadata().Name).
		Any("architecture", f.Architecture().Architecture).Msgf("Model file loaded: %s", cfg.ModelFileName())

	// guess the name
	if cfg.Name == "" {
		cfg.Name = f.Metadata().Name
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
		if cfg.RepeatPenalty == 0.0 {
			cfg.RepeatPenalty = settings.RepeatPenalty
		}
	} else {
		log.Debug().Any("family", family).Msgf("guessDefaultsFromFile: no template found for family")
	}

	if cfg.HasTemplate() {
		return
	}

	// identify from well known templates first, otherwise use the raw jinja template
	chatTemplate, found := f.Header.MetadataKV.Get("tokenizer.chat_template")
	if found {
		// try to use the jinja template
		cfg.TemplateConfig.JinjaTemplate = true
		cfg.TemplateConfig.ChatMessage = chatTemplate.ValueString()
	}

}

func identifyFamily(f *gguf.GGUFFile) familyType {

	// identify from well known templates first
	chatTemplate, found := f.Header.MetadataKV.Get("tokenizer.chat_template")
	if found && chatTemplate.ValueString() != "" {
		if family, ok := knownTemplates[chatTemplate.ValueString()]; ok {
			return family
		}
	}

	// otherwise try to identify from the model properties
	arch := f.Architecture().Architecture
	eosTokenID := f.Tokenizer().EOSTokenID
	bosTokenID := f.Tokenizer().BOSTokenID

	isYI := arch == "llama" && bosTokenID == 1 && eosTokenID == 2
	// WTF! Mistral0.3 and isYi have same bosTokenID and eosTokenID

	llama3 := arch == "llama" && eosTokenID == 128009
	commandR := arch == "command-r" && eosTokenID == 255001
	qwen2 := arch == "qwen2"
	phi3 := arch == "phi-3"
	gemma := strings.HasPrefix(arch, "gemma") || strings.Contains(strings.ToLower(f.Metadata().Name), "gemma")
	deepseek2 := arch == "deepseek2"

	switch {
	case deepseek2:
		return DeepSeek2
	case gemma:
		return Gemma
	case llama3:
		return LLaMa3
	case commandR:
		return CommandR
	case phi3:
		return Phi3
	case qwen2, isYI:
		return ChatML
	default:
		return Unknown
	}
}
