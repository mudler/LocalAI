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
			// {other} names a connection and is filled by Duplicate.
			if strings.Contains(strings.ReplaceAll(got, "{other}", ""), "{") {
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

// The sentence about two connections of one bot names the other connection in
// every language, so the owner reads which one it is.
func TestDuplicateNamesTheOtherConnection(t *testing.T) {
	english := studio.DuplicateMessage("Work bot")
	if !strings.Contains(english, "Work bot") || strings.Contains(english, "{") {
		t.Fatalf("English: %q", english)
	}
	for _, lang := range connectortext.Languages {
		got, ok := connectortext.Duplicate(lang, "Work bot")
		if !ok || !strings.Contains(got, "Work bot") || strings.Contains(got, "{") {
			t.Errorf("%s: %q", lang, got)
		}
		if got == english {
			t.Errorf("%s: not translated", lang)
		}
	}
	if _, ok := connectortext.Duplicate("xx", "a"); ok {
		t.Error("an unknown language must have no text")
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
	for _, code := range studio.ToolReasonCodes() {
		known[connectortext.ToolReasonKey(code)] = true
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

// Every reason of an add-on tool outcome has a sentence in every language, with
// {status} where English has it, and an unknown code has none.
func TestEveryToolReasonIsInEveryLanguage(t *testing.T) {
	for _, code := range studio.ToolReasonCodes() {
		english := studio.ToolReasonMessage(code, 403)
		if english == "" || strings.Contains(english, "{") {
			t.Errorf("code %q has no complete English sentence: %q", code, english)
		}
		for _, lang := range connectortext.Languages {
			got, ok := connectortext.ToolReason(lang, code, 403)
			if !ok || strings.TrimSpace(got) == "" {
				t.Errorf("%s: tool reason %q has no text", lang, code)
				continue
			}
			if strings.Contains(got, "{") {
				t.Errorf("%s: tool reason %q leaves a placeholder: %q", lang, code, got)
			}
			if got == english {
				t.Errorf("%s: tool reason %q is not translated", lang, code)
			}
			if code == "upstream" && !strings.Contains(got, "403") {
				t.Errorf("%s: the generic upstream sentence lost the status: %q", lang, got)
			}
		}
	}
	for _, lang := range connectortext.Languages {
		if _, ok := connectortext.ToolReason(lang, "no_such_reason", 0); ok {
			t.Errorf("%s: an unknown reason must have no text", lang)
		}
	}
}

// A reason from the host maps to a sentence that exists.
func TestToolReasonKeyAlwaysHasText(t *testing.T) {
	for _, reason := range []string{"upstream_401", "upstream_403", "upstream_404", "upstream_422", "upstream_500", "upstream_429", "unknown_tool", "canceled", "write_mode_off", "sender_needs_pairing", "needs_confirmation"} {
		key := studio.ToolReasonKey(reason)
		if studio.ToolReasonMessage(reason, 500) == "" {
			t.Errorf("%s: no English sentence", reason)
		}
		for _, lang := range connectortext.Languages {
			if slices.Contains(studio.BlockCodes(), key) {
				if _, ok := connectortext.Block(lang, key, contract.CapToolsWrite); !ok {
					t.Errorf("%s: %s (%s) has no block text", lang, reason, key)
				}
			} else if _, ok := connectortext.ToolReason(lang, key, 500); !ok {
				t.Errorf("%s: %s (%s) has no tool text", lang, reason, key)
			}
		}
	}
}
