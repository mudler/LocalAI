package main

import (
	"strconv"
	"strings"
)

// loadOptions holds the parsed model-level options.
type loadOptions struct {
	codecPath string
	useFA     bool
	clampFP16 bool
	seed      int64
}

// sampling holds per-request generation parameters with qt defaults applied.
type sampling struct {
	temperature float32
	topK        int
	topP        float32
	repPen      float32
	maxNew      int
	seed        int64
}

func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

func parseBool(v string) bool { return v == "true" || v == "1" }

// parseOptions reads the backend "key:value" option slice. Unknown keys are
// ignored. Defaults: use_fa true (qt default; CPU still uses the F32 chain),
// seed -1 (engine random).
func parseOptions(opts []string) loadOptions {
	o := loadOptions{useFA: true, seed: -1}
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "tokenizer", "codec":
			o.codecPath = value
		case "use_fa":
			o.useFA = parseBool(value)
		case "clamp_fp16":
			o.clampFP16 = parseBool(value)
		case "seed":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				o.seed = n
			}
		}
	}
	return o
}

// languageAliases maps codes / locales / full names to the upstream qwentts
// language names. "auto" (and empty) map to "" so the engine auto-detects.
var languageAliases = map[string]string{
	"en": "english", "english": "english",
	"zh": "chinese", "chinese": "chinese", "mandarin": "chinese",
	"ja": "japanese", "japanese": "japanese",
	"ko": "korean", "korean": "korean",
	"de": "german", "german": "german",
	"fr": "french", "french": "french",
	"es": "spanish", "spanish": "spanish",
	"it": "italian", "italian": "italian",
	"pt": "portuguese", "portuguese": "portuguese",
	"ru": "russian", "russian": "russian",
	"auto": "",
}

// normalizeLanguage lowercases, trims, strips a region/locale suffix
// (en-US -> en), and resolves to the qwentts language name. Empty stays empty
// (engine auto-detects); an unknown value passes through normalized.
func normalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return ""
	}
	if i := strings.IndexAny(lang, "-_."); i >= 0 {
		lang = lang[:i]
	}
	if v, ok := languageAliases[lang]; ok {
		return v
	}
	return lang
}

var refAudioExts = []string{".wav", ".flac", ".mp3", ".ogg", ".m4a"}

// resolveVoice interprets the request Voice field: a value ending in a known
// audio extension is a clone-reference path; anything else is a named speaker
// (custom_voice). Empty input yields no speaker and no reference.
func resolveVoice(voice string) (speaker, refPath string) {
	v := strings.TrimSpace(voice)
	if v == "" {
		return "", ""
	}
	lower := strings.ToLower(v)
	for _, ext := range refAudioExts {
		if strings.HasSuffix(lower, ext) {
			return "", v
		}
	}
	return v, ""
}

func parseFloat32(v string, def float32) float32 {
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		return def
	}
	return float32(f)
}

func parseInt(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func parseInt64(v string, def int64) int64 {
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// parseSampling reads per-request sampling params from the TTSRequest params
// map, applying qt defaults (matching qt_tts_default_params).
func parseSampling(params map[string]string, defaultSeed int64) sampling {
	s := sampling{temperature: 0.9, topK: 50, topP: 1.0, repPen: 1.05, maxNew: 2048, seed: defaultSeed}
	if params == nil {
		return s
	}
	s.temperature = parseFloat32(params["temperature"], s.temperature)
	s.topK = parseInt(params["top_k"], s.topK)
	s.topP = parseFloat32(params["top_p"], s.topP)
	s.repPen = parseFloat32(params["repetition_penalty"], s.repPen)
	s.maxNew = parseInt(params["max_new_tokens"], s.maxNew)
	s.seed = parseInt64(params["seed"], s.seed)
	return s
}
