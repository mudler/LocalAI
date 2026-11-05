package connectortext_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectortext"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Every capability and every refusal code has a text in every language. A new
// code or capability fails here until all the languages carry it.
func TestEveryCodeIsInEveryLanguage(t *testing.T) {
	if len(connectortext.Languages) != 8 {
		t.Fatalf("want 8 languages, got %v", connectortext.Languages)
	}
	for _, lang := range connectortext.Languages {
		for _, name := range contract.ConnectorCapabilityNames() {
			title, desc, ok := connectortext.Capability(lang, name)
			if !ok || strings.TrimSpace(title) == "" || strings.TrimSpace(desc) == "" {
				t.Errorf("%s: capability %q has no title or description", lang, name)
			}
			if a, ok := connectortext.Action(lang, name); !ok || strings.TrimSpace(a) == "" {
				t.Errorf("%s: capability %q has no action", lang, name)
			}
		}
		for _, code := range studio.BlockCodes() {
			got, ok := connectortext.Block(lang, code, contract.CapMemoryWrite)
			if !ok || strings.TrimSpace(got) == "" {
				t.Errorf("%s: block code %q has no text", lang, code)
				continue
			}
			if strings.Contains(got, "{") {
				t.Errorf("%s: block code %q leaves a placeholder: %q", lang, code, got)
			}
		}
		if _, ok := connectortext.Block(lang, "no_such_code", ""); ok {
			t.Errorf("%s: an unknown code must have no text", lang)
		}
	}
}

// The translated sentence has {action} exactly where the English one has it,
// so a sentence never loses the verb or shows the raw name.
func TestPlaceholdersMatchEnglish(t *testing.T) {
	for _, code := range studio.BlockCodes() {
		english := studio.BlockOwnerMessage(code, contract.CapMemoryWrite)
		if english == "" {
			t.Errorf("code %q has no English message", code)
			continue
		}
		wantAction := strings.Contains(english, "save notes to memory")
		for _, lang := range connectortext.Languages {
			filled, _ := connectortext.Block(lang, code, contract.CapMemoryWrite)
			action, _ := connectortext.Action(lang, contract.CapMemoryWrite)
			if got := strings.Contains(filled, action); got != wantAction {
				t.Errorf("%s: code %q uses the action = %v, English uses it = %v", lang, code, got, wantAction)
			}
		}
	}
}

func TestNoLocaleHasAnExtraKey(t *testing.T) {
	// Locale keys outside the closed lists would be dead text.
	known := map[string]bool{"action.unknown": true}
	for _, name := range contract.ConnectorCapabilityNames() {
		for _, f := range []string{"title", "description", "action"} {
			known[connectortext.CapabilityKey(name, f)] = true
		}
	}
	for _, code := range studio.BlockCodes() {
		known[connectortext.BlockKey(code)] = true
	}
	for _, lang := range connectortext.Languages {
		for _, k := range connectortext.KeysOf(lang) {
			if !known[k] {
				t.Errorf("%s: key %q matches no capability or code", lang, k)
			}
		}
	}
}

func TestNegotiate(t *testing.T) {
	cases := []struct{ explicit, accept, want string }{
		{"it", "", "it"},
		{"pt", "", "pt-BR"},
		{"zh", "", "zh-CN"},
		{"zh-Hans", "", "zh-CN"},
		{"", "ko-KR,ko;q=0.9,en;q=0.5", "ko"},
		{"it", "de", "it"},
		{"en", "de", ""},
		{"xx", "de", ""},
		{"", "", ""},
		{"", "en-US,it;q=0.5", ""},
	}
	for _, c := range cases {
		if got := connectortext.Negotiate(c.explicit, c.accept); got != c.want {
			t.Errorf("Negotiate(%q, %q) = %q, want %q", c.explicit, c.accept, got, c.want)
		}
	}
	if !slices.Contains(connectortext.Languages, "id") {
		t.Error("id is missing")
	}
}
