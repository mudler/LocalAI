// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxI18nChars bounds the characters of all translated text of one manifest.
// The manifest as a whole is bound by Limits.ManifestBytes; this keeps the
// translations a small part of it.
const MaxI18nChars = 12000

// maxAcceptLanguageBytes bounds the header that NegotiateLanguage reads.
const maxAcceptLanguageBytes = 1024

// Translation is the text of one language for the parts of a manifest that
// the ui.title and ui.description language maps do not reach. The English
// fields stay the source of truth and the fallback: a translation holds no
// field that the English manifest lacks, and a field it leaves out is shown
// in English.
//
// The set of fields is closed on purpose. Only text that the host draws for a
// person is translatable. Tool descriptions, input schemas and skills are
// written for a model and stay English, and an unknown field fails the strict
// parser, as it does on a host that does not know this field.
type Translation struct {
	// Kinds translates connector kinds, by kind ID.
	Kinds map[string]KindText `json:"kinds,omitempty"`
	// Resources translates the noun of the resources (feature level 2).
	Resources *ResourceText `json:"resources,omitempty"`
}

// ResourceText is the translated noun of the resources of an add-on, singular
// and plural. A field left out is shown in the next fallback (see
// Manifest.ResourceNouns).
type ResourceText struct {
	Noun       string `json:"noun,omitempty"`
	NounPlural string `json:"nounPlural,omitempty"`
}

// KindText is the translated text of one connector kind. A field left out is
// shown in English.
type KindText struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// validateI18n checks the translations against the manifest.
func validateI18n(m Manifest) error {
	if len(m.I18n) == 0 {
		if m.I18n != nil {
			return fmt.Errorf("i18n has no language")
		}
		return nil
	}
	if len(m.I18n) > MaxLanguages {
		return fmt.Errorf("i18n has more than %d languages", MaxLanguages)
	}
	kinds := map[string]ConnectorKind{}
	if m.Capabilities.Connector != nil {
		for _, k := range m.Capabilities.Connector.Kinds {
			kinds[k.ID] = k
		}
	}
	seen := map[string]bool{}
	total := 0
	for tag, t := range m.I18n {
		key := strings.ToLower(tag)
		if len(tag) > 35 || !languageTag.MatchString(tag) || seen[key] {
			return fmt.Errorf("i18n language %q is not a distinct language tag", tag)
		}
		if key == "en" || strings.HasPrefix(key, "en-") {
			return fmt.Errorf("i18n language %q: English is the manifest itself", tag)
		}
		seen[key] = true
		if len(t.Kinds) == 0 && t.Resources == nil {
			return fmt.Errorf("i18n language %q translates nothing", tag)
		}
		if rt := t.Resources; rt != nil {
			rs := m.Capabilities.Resources
			if rs == nil || rs.Noun == "" || *rt == (ResourceText{}) {
				return fmt.Errorf("i18n %q translates resources that the manifest does not name", tag)
			}
			for _, w := range []string{rt.Noun, rt.NounPlural} {
				if w != "" && !plainText(w, 40) {
					return fmt.Errorf("i18n %q: invalid resource noun", tag)
				}
				total += utf8.RuneCountInString(w)
			}
		}
		for id, text := range t.Kinds {
			k, ok := kinds[id]
			if !ok {
				return fmt.Errorf("i18n %q translates the connector kind %q that the manifest does not declare", tag, id)
			}
			if text == (KindText{}) {
				return fmt.Errorf("i18n %q kind %q has no text", tag, id)
			}
			if text.Title != "" && !plainText(text.Title, MaxTitleChars) {
				return fmt.Errorf("i18n %q kind %q: invalid title", tag, id)
			}
			if text.Description != "" && (k.Description == "" || !plainText(text.Description, MaxDescriptionChars)) {
				return fmt.Errorf("i18n %q kind %q: invalid description", tag, id)
			}
			total += utf8.RuneCountInString(text.Title) + utf8.RuneCountInString(text.Description)
		}
	}
	if total > MaxI18nChars {
		return fmt.Errorf("i18n text exceeds %d characters", MaxI18nChars)
	}
	return nil
}

// Languages returns the language tags of the map other than English, sorted.
func (l Localized) Languages() []string {
	var out []string
	for tag := range l.ByLanguage {
		if !isEnglish(tag) {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

// For returns the text for a language tag: the plain string, the entry the
// tag matches (see MatchLanguage), else Default. An empty tag is English.
func (l Localized) For(tag string) string {
	if l.ByLanguage != nil && tag != "" {
		if got, ok := MatchLanguage(tag, l.Languages()); ok {
			return l.ByLanguage[got]
		}
	}
	return l.Default()
}

// KindLanguages returns the languages in which the manifest translates at
// least part of a connector kind, sorted.
func (m Manifest) KindLanguages() []string {
	out := make([]string, 0, len(m.I18n))
	for tag := range m.I18n {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// KindTranslations returns the translations of one connector kind, by
// language tag. It is nil when no language translates the kind.
func (m Manifest) KindTranslations(kindID string) map[string]KindText {
	var out map[string]KindText
	for tag, t := range m.I18n {
		if text, ok := t.Kinds[kindID]; ok {
			if out == nil {
				out = map[string]KindText{}
			}
			out[tag] = text
		}
	}
	return out
}

// KindText returns the translated title and description of a connector kind
// for a language tag. A field that the translation lacks is the English text.
func (m Manifest) KindText(kindID, tag string) (title, description string) {
	if m.Capabilities.Connector == nil {
		return "", ""
	}
	for _, k := range m.Capabilities.Connector.Kinds {
		if k.ID != kindID {
			continue
		}
		title, description = k.Title, k.Description
		if got, ok := MatchLanguage(tag, m.KindLanguages()); ok {
			if t := m.I18n[got].Kinds[kindID]; t != (KindText{}) {
				if t.Title != "" {
					title = t.Title
				}
				if t.Description != "" {
					description = t.Description
				}
			}
		}
		return title, description
	}
	return "", ""
}

// ResourceNouns returns the singular and plural noun of the resources for a
// language tag. A translation that gives only the singular is not mixed with
// the English plural: the plural then equals its singular. Without a
// matching translation the English plural is used, else the English noun.
func (m Manifest) ResourceNouns(tag string) (noun, plural string) {
	r := m.Capabilities.Resources
	if r == nil {
		return "", ""
	}
	texts := map[string]ResourceText{}
	for t, v := range m.I18n {
		if v.Resources != nil {
			texts[t] = *v.Resources
		}
	}
	return ResolveResourceNouns(r.Noun, r.NounPlural, texts, tag)
}

// ResolveResourceNouns is the rule of Manifest.ResourceNouns for a host that
// holds the English nouns and the translations apart from the manifest.
func ResolveResourceNouns(noun, plural string, translations map[string]ResourceText, tag string) (string, string) {
	if plural == "" {
		plural = noun
	}
	langs := make([]string, 0, len(translations))
	for t := range translations {
		langs = append(langs, t)
	}
	if got, ok := MatchLanguage(tag, langs); ok {
		t := translations[got]
		if t.Noun != "" {
			noun, plural = t.Noun, t.Noun
		}
		if t.NounPlural != "" {
			plural = t.NounPlural
		}
	}
	return noun, plural
}

// ValidLanguageTag reports whether s is a language tag a manifest may use: a
// BCP 47 subset of a two or three letter language and up to three subtags.
func ValidLanguageTag(s string) bool { return len(s) <= 35 && languageTag.MatchString(s) }

func isEnglish(tag string) bool {
	t := strings.ToLower(tag)
	return t == "en" || strings.HasPrefix(t, "en-")
}

// MatchLanguage finds the available tag that serves a requested tag: the same
// tag, else the tag with its trailing subtags removed, else the first
// available tag (in sorted order) with the same primary language. Case does
// not matter; the available tag is returned as written. English and an empty
// request match nothing, because English is the manifest's own text.
func MatchLanguage(request string, available []string) (string, bool) {
	if request == "" || isEnglish(request) || !languageTag.MatchString(request) || len(request) > 35 {
		return "", false
	}
	sorted := slices.Clone(available)
	sort.Strings(sorted)
	want := strings.ToLower(request)
	for {
		for _, a := range sorted {
			if strings.ToLower(a) == want {
				return a, true
			}
		}
		i := strings.LastIndex(want, "-")
		if i < 0 {
			break
		}
		want = want[:i]
	}
	for _, a := range sorted {
		if strings.ToLower(strings.SplitN(a, "-", 2)[0]) == want {
			return a, true
		}
	}
	return "", false
}

// NegotiateLanguage picks the available tag for a request. The explicit
// language (a lang query parameter) wins; without one the Accept-Language
// header is read in order of its quality values; without either the answer is
// English. English is the empty answer: a caller then keeps the English
// fields. An explicit language that nothing matches is English too; the
// header is not consulted behind it. A malformed or oversized input is
// English.
func NegotiateLanguage(explicit, acceptLanguage string, available []string) string {
	if len(available) == 0 {
		return ""
	}
	if explicit != "" {
		got, _ := MatchLanguage(explicit, available)
		return got
	}
	if acceptLanguage == "" || len(acceptLanguage) > maxAcceptLanguageBytes {
		return ""
	}
	type choice struct {
		tag string
		q   float64
		at  int
	}
	var choices []choice
	for i, part := range strings.Split(acceptLanguage, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag, q := strings.TrimSpace(fields[0]), 1.0
		if tag == "" {
			continue
		}
		ok := true
		for _, f := range fields[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(f), "=")
			if !found || strings.TrimSpace(name) != "q" {
				ok = false
				break
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || v < 0 || v > 1 {
				ok = false
				break
			}
			q = v
		}
		if ok && q > 0 {
			choices = append(choices, choice{tag, q, i})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].q > choices[j].q })
	for _, c := range choices {
		if c.tag == "*" || isEnglish(c.tag) {
			return ""
		}
		if got, ok := MatchLanguage(c.tag, available); ok {
			return got
		}
	}
	return ""
}
