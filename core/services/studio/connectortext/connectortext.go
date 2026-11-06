// Package connectortext holds the texts that the connection settings page
// shows the owner for a capability or a refusal, in the languages of the web
// UI. English is the source and stays in the code that decides (the capability
// list of pkg/addons/contract and the block table of the studio service); this
// package holds only the other languages, by code, so the API can answer in the
// language the caller asked for while the code of each item stays the same.
//
// The text that goes to the sender of a chat or to the model is not here: the
// host speaks to a sender through hostmsg, and the model reads English.
package connectortext

import (
	"embed"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Languages lists the translated languages. English is the source and has no
// file.
var Languages = []string{"it", "de", "es", "fr", "id", "ko", "pt-BR", "zh-CN"}

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
			panic("connectortext: missing locale " + l)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic("connectortext: bad locale " + l + ": " + err.Error())
		}
		catalog[l] = m
	}
}

func lookup(lang, key string) (string, bool) {
	loadOnce.Do(load)
	s, ok := catalog[lang][key]
	return s, ok && s != ""
}

// Negotiate picks the language for a request: the explicit tag (a lang query
// parameter) wins, then Accept-Language. It returns "" for English, for an
// unknown language and for no preference; the caller then keeps the English
// fields.
func Negotiate(explicit, acceptLanguage string) string {
	return contract.NegotiateLanguage(explicit, acceptLanguage, Languages)
}

// Key builders, shared with the tests.
func CapabilityKey(name, field string) string { return "cap." + name + "." + field }
func BlockKey(code string) string             { return "block." + code }

// ToolReasonKey is the key of the sentence for the reason of an add-on tool
// outcome (see studio.ToolReasonCodes).
func ToolReasonKey(code string) string { return "tool." + code }

// ToolReason returns the sentence for the reason of an add-on tool outcome in a
// language, with {status} filled in. It reports false when the language lacks
// the text; the caller then keeps the English sentence.
func ToolReason(lang, code string, status int) (string, bool) {
	tpl, ok := lookup(lang, ToolReasonKey(code))
	if !ok {
		return "", false
	}
	return strings.ReplaceAll(tpl, "{status}", strconv.Itoa(status)), true
}

// Capability returns the title and description of a capability in a language.
// It reports false when the language or the capability has no text.
func Capability(lang, name string) (title, description string, ok bool) {
	t, ok1 := lookup(lang, CapabilityKey(name, "title"))
	d, ok2 := lookup(lang, CapabilityKey(name, "description"))
	return t, d, ok1 && ok2
}

// Action returns the verb phrase of a capability in a language ("set
// reminders"), or false.
func Action(lang, capability string) (string, bool) {
	return lookup(lang, CapabilityKey(capability, "action"))
}

// Block returns the owner's sentence for a refusal code in a language, with
// {action} filled in for the capability. It reports false when the language
// lacks the text; the caller then keeps the English sentence.
func Block(lang, code, capability string) (string, bool) {
	tpl, ok := lookup(lang, BlockKey(code))
	if !ok {
		return "", false
	}
	action, ok := Action(lang, capability)
	if !ok {
		action, _ = lookup(lang, "action.unknown")
	}
	return strings.ReplaceAll(tpl, "{action}", action), true
}

// Duplicate returns the sentence of the code duplicate_connection in a
// language, with the name of the other connection filled in. It reports false
// when the language lacks the text; the caller then keeps the English sentence.
func Duplicate(lang, other string) (string, bool) {
	tpl, ok := lookup(lang, BlockKey("duplicate_connection"))
	if !ok {
		return "", false
	}
	return strings.ReplaceAll(tpl, "{other}", other), true
}

// KeysOf lists the keys of a language, for the tests that guard the files.
func KeysOf(lang string) []string {
	loadOnce.Do(load)
	out := make([]string, 0, len(catalog[lang]))
	for k := range catalog[lang] {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
