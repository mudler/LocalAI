// Package hostmsg holds the sentences that the host writes to people in the
// chats of messaging apps: the confirmations, refusals, notes about files and
// the text of proposals. Every such sentence is a key of a catalog with one
// template per language, so the person reads it in their own language.
//
// Text for the model (tool descriptions, notes of a turn, prompts) is not here
// and stays English: the model answers in the language of the conversation.
package hostmsg

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The languages of the host messages. English is the source and the fallback.
const (
	English    = "en"
	Italian    = "it"
	German     = "de"
	Spanish    = "es"
	French     = "fr"
	Portuguese = "pt-BR"

	// Auto is the setting that follows the language of the sender.
	Auto = "auto"
)

// Languages lists the language codes, English first.
var Languages = []string{English, Italian, German, Spanish, French, Portuguese}

//go:embed locales/*.json
var localeFS embed.FS

var (
	loadOnce sync.Once
	catalog  map[string]map[string]string
)

func load() {
	catalog = map[string]map[string]string{}
	for _, l := range Languages {
		raw, err := localeFS.ReadFile("locales/" + l + ".json")
		if err != nil {
			panic("hostmsg: missing locale " + l)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic("hostmsg: bad locale " + l + ": " + err.Error())
		}
		catalog[l] = m
	}
}

func table(lang string) map[string]string {
	loadOnce.Do(load)
	return catalog[lang]
}

// Normalize maps a language tag ("it", "it-IT", "pt_br", "PT") to a language
// code of the catalog. It reports false for a language the catalog lacks.
func Normalize(tag string) (string, bool) {
	t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	base, _, _ := strings.Cut(t, "-")
	switch base {
	case "en":
		return English, true
	case "it":
		return Italian, true
	case "de":
		return German, true
	case "es":
		return Spanish, true
	case "fr":
		return French, true
	case "pt":
		return Portuguese, true
	}
	return "", false
}

// ValidSetting says whether s is a value of the reply language setting of a
// connection: "auto", or the exact code of a language. The empty string is
// "auto" and also valid.
func ValidSetting(s string) bool {
	if s == "" || s == Auto {
		return true
	}
	for _, l := range Languages {
		if s == l {
			return true
		}
	}
	return false
}

// Keys lists the keys of the catalog, sorted.
func Keys() []string {
	t := table(English)
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Has says whether the key is in the catalog.
func Has(key string) bool {
	_, ok := table(English)[key]
	return ok
}

// Template returns the template of a key in a language. A language that lacks
// the key (the tests forbid that) falls back to English, and a key that is
// not in the catalog comes back as itself.
func Template(lang, key string) string {
	if s, ok := table(lang)[key]; ok && s != "" {
		return s
	}
	if s, ok := table(English)[key]; ok {
		return s
	}
	return key
}

var placeholderRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// Placeholders lists the names in a template, sorted and without repeats.
func Placeholders(tpl string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(tpl, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// Render fills the {name} places of a template. args are name and value pairs.
// The values are not read again, so a value that holds braces stays as it is.
// A place without an argument stays in the text.
func Render(tpl string, args ...any) string {
	if len(args) == 0 {
		return tpl
	}
	vals := make(map[string]string, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		vals[fmt.Sprint(args[i])] = fmt.Sprint(args[i+1])
	}
	return placeholderRE.ReplaceAllStringFunc(tpl, func(m string) string {
		if v, ok := vals[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// Voice speaks the catalog in one language. The zero value speaks English.
type Voice struct{ Lang string }

// T renders a key in the language of the voice.
func (v Voice) T(key string, args ...any) string {
	lang := v.Lang
	if lang == "" {
		lang = English
	}
	return Render(Template(lang, key), args...)
}

// In returns the voice for a language code, English for an unknown one.
func In(lang string) Voice {
	if l, ok := Normalize(lang); ok {
		return Voice{Lang: l}
	}
	return Voice{Lang: English}
}

// Join lists words with commas and the word for "and" before the last one.
func (v Voice) Join(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + v.T("join.and") + words[len(words)-1]
}

// KeysOf lists the keys that one language has. The tests use it to find a key
// that a translation has and English lacks.
func KeysOf(lang string) []string {
	t := table(lang)
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
