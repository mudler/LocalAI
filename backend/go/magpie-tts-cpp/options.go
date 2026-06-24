package main

import (
	"fmt"
	"strconv"
	"strings"
)

// loadOptions holds the parsed model-level options. Magpie is a single
// self-contained GGUF (model + codec + tokenizer + G2P dictionaries), so the
// options only cover synthesis defaults.
type loadOptions struct {
	// speaker is the default baked speaker when a request has no voice.
	speaker string
	// language is the default language when a request has none ("" = engine
	// default, which is "en").
	language string
}

func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

// parseOptions reads the backend "key:value" option slice. Unknown keys are
// ignored.
func parseOptions(opts []string) loadOptions {
	var o loadOptions
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "speaker", "voice":
			o.speaker = value
		case "language", "lang":
			o.language = value
		}
	}
	return o
}

// magpieSpeakers are the baked speakers of Magpie TTS Multilingual 357M, in
// index order (the engine matches names exactly, so the Go side canonicalizes
// case-insensitive names and 0-4 indices to these strings).
var magpieSpeakers = []string{"Aria", "Jason", "John", "Leo", "Sofia"}

// resolveSpeaker maps the request voice (falling back to the model-level
// default) onto a canonical baked speaker name. Accepted forms:
// case-insensitive names (aria, JASON, ...) and indices 0-4. Empty selects the
// engine default (speaker 0, Aria). Anything else is rejected with the valid
// choices, instead of surfacing the engine's late error.
func resolveSpeaker(voice, fallback string) (string, error) {
	v := strings.TrimSpace(voice)
	if v == "" {
		v = strings.TrimSpace(fallback)
	}
	if v == "" {
		return "", nil
	}
	if idx, err := strconv.Atoi(v); err == nil {
		if idx < 0 || idx >= len(magpieSpeakers) {
			return "", fmt.Errorf("magpie-tts: speaker index %d out of range 0-%d", idx, len(magpieSpeakers)-1)
		}
		return magpieSpeakers[idx], nil
	}
	for _, s := range magpieSpeakers {
		if strings.EqualFold(s, v) {
			return s, nil
		}
	}
	return "", fmt.Errorf("magpie-tts: unknown voice %q (valid: %s, or 0-%d)",
		voice, strings.Join(magpieSpeakers, ", "), len(magpieSpeakers)-1)
}

// magpieLanguages are the canonical language codes the tokenizer's language
// map knows (exact-match on the C side), keyed by their lowercase form so
// requests can be case-insensitive.
var magpieLanguages = map[string]string{
	"en": "en", "es": "es", "de": "de", "fr": "fr", "it": "it",
	"pt-br": "pt-BR", "hi": "hi", "vi": "vi", "ko": "ko",
	"ar-ae": "ar-AE", "ar-sa": "ar-SA", "ar-msa": "ar-MSA",
}

// resolveLanguage picks the request language, else the model-level default,
// else "" (the engine defaults to "en"), canonicalizing case for the known
// codes. Unknown codes pass through verbatim so the engine reports them with
// its own exact-vocabulary error.
func resolveLanguage(reqLang *string, fallback string) string {
	l := ""
	if reqLang != nil {
		l = strings.TrimSpace(*reqLang)
	}
	if l == "" {
		l = strings.TrimSpace(fallback)
	}
	if l == "" {
		return ""
	}
	if canon, ok := magpieLanguages[strings.ToLower(l)]; ok {
		return canon
	}
	return l
}
