package main

import (
	"strconv"
	"strings"
)

// loadOptions holds the parsed model-level options for OmniVoice.
type loadOptions struct {
	codecPath string
	useFA     bool
	clampFP16 bool
	seed      int64
	denoise   bool
}

func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

// parseOptions reads the backend "key:value" option slice. Unknown keys are
// ignored. Defaults: seed -1 (engine default), denoise true.
func parseOptions(opts []string) loadOptions {
	o := loadOptions{seed: -1, denoise: true}
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "tokenizer", "codec":
			o.codecPath = value
		case "use_fa":
			o.useFA = value == "true" || value == "1"
		case "clamp_fp16":
			o.clampFP16 = value == "true" || value == "1"
		case "seed":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				o.seed = n
			}
		case "denoise":
			o.denoise = value == "true" || value == "1"
		}
	}
	return o
}

// languageNameAliases maps full language names to OmniVoice codes. OmniVoice's
// lang hint accepts "" (auto), "en", "zh" per the upstream convention; other
// codes pass through and the engine treats unknown hints as auto.
var languageNameAliases = map[string]string{
	"english": "en",
	"chinese": "zh",
}

// normalizeLanguage lowercases, trims, strips a region/locale suffix, and
// resolves common full names. Empty stays empty so the engine auto-detects.
func normalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return ""
	}
	if i := strings.IndexAny(lang, "-_."); i >= 0 {
		lang = lang[:i]
	}
	if code, ok := languageNameAliases[lang]; ok {
		return code
	}
	return lang
}
