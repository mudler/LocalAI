package main

import (
	"strconv"
	"strings"
)

// loadOptions holds the parsed model-level options. MOSS-TTS-Local needs three
// GGUFs (local transformer, audio codec, text tokenizer); the model path is the
// local transformer and codec/tokenizer are resolved as siblings or via these
// options.
type loadOptions struct {
	codecPath     string
	tokenizerPath string
	seed          int
}

func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

// parseOptions reads the backend "key:value" option slice. Unknown keys are
// ignored. Default seed is -1 (engine random).
func parseOptions(opts []string) loadOptions {
	o := loadOptions{seed: -1}
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "codec", "audio_tokenizer":
			o.codecPath = value
		case "tokenizer", "text_tokenizer":
			o.tokenizerPath = value
		case "seed":
			if n, err := strconv.Atoi(value); err == nil {
				o.seed = n
			}
		}
	}
	return o
}

var refAudioExts = []string{".wav", ".flac", ".mp3", ".ogg", ".m4a"}

// resolveVoice interprets the request Voice field. MOSS-TTS-Local has no named
// speakers, only reference-audio cloning, so a value ending in a known audio
// extension is treated as a clone-reference path and anything else is ignored
// (the caller then falls back to the config audio_path).
func resolveVoice(voice string) (refPath string) {
	v := strings.TrimSpace(voice)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	for _, ext := range refAudioExts {
		if strings.HasSuffix(lower, ext) {
			return v
		}
	}
	return ""
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
