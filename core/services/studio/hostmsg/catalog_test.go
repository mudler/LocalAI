package hostmsg_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
)

func TestEveryKeyIsInEveryLanguage(t *testing.T) {
	en := hostmsg.Keys()
	if len(en) < 100 {
		t.Fatalf("the catalog looks empty: %d keys", len(en))
	}
	for _, lang := range hostmsg.Languages {
		for _, k := range en {
			tpl := hostmsg.Template(lang, k)
			if strings.TrimSpace(tpl) == "" || tpl == k {
				t.Errorf("%s: key %q is missing or empty", lang, k)
				continue
			}
			want := hostmsg.Placeholders(hostmsg.Template(hostmsg.English, k))
			if got := hostmsg.Placeholders(tpl); !slices.Equal(got, want) {
				t.Errorf("%s: key %q has placeholders %v, English has %v", lang, k, got, want)
			}
			if lang != hostmsg.English && tpl == hostmsg.Template(hostmsg.English, k) && len(tpl) > 40 {
				t.Errorf("%s: key %q is still the English text", lang, k)
			}
		}
	}
}

func TestNoLanguageHasExtraKeys(t *testing.T) {
	for _, lang := range hostmsg.Languages {
		for _, k := range hostmsg.KeysOf(lang) {
			if !hostmsg.Has(k) {
				t.Errorf("%s has key %q that English lacks", lang, k)
			}
		}
	}
}

func TestRenderFillsNamesOnce(t *testing.T) {
	got := hostmsg.Render("{a} and {b} and {c}", "a", "{b}", "b", 7)
	if got != "{b} and 7 and {c}" {
		t.Fatalf("got %q", got)
	}
	if got := hostmsg.In("it").T("new.done"); !strings.HasPrefix(got, "Nuova chat") {
		t.Fatalf("got %q", got)
	}
	if got := (hostmsg.Voice{}).T("proposal.cancelled"); got != "Cancelled." {
		t.Fatalf("the zero voice must speak English, got %q", got)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"it": "it", "it-IT": "it", "PT_br": "pt-BR", "pt": "pt-BR", "de-AT": "de", "en-GB": "en", "fr": "fr", "es-MX": "es"} {
		if got, ok := hostmsg.Normalize(in); !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := hostmsg.Normalize("ja"); ok {
		t.Error("ja is not a language of the catalog")
	}
}

func TestValidSetting(t *testing.T) {
	for _, s := range []string{"", "auto", "en", "it", "de", "es", "fr", "pt-BR"} {
		if !hostmsg.ValidSetting(s) {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, s := range []string{"pt", "IT", "ja", "it-IT", "auto "} {
		if hostmsg.ValidSetting(s) {
			t.Errorf("%q must be refused", s)
		}
	}
}

// The Italian sentences are written by hand and checked word by word. A change
// to one of them must be a decision, so eight of the sentences people read most
// are pinned here.
func TestItalianGolden(t *testing.T) {
	it := hostmsg.In("it")
	for _, c := range []struct {
		key  string
		args []any
		want string
	}{
		{"new.done", nil, "Nuova chat avviata. Non vedrò più i nostri messaggi precedenti."},
		{"rate", nil, "Troppi messaggi: attendi un minuto."},
		{"pairing.bad", nil, "Questo codice non è valido. Potrebbe essere scaduto o già usato. Chiedine uno nuovo."},
		{"proposal.cancelled", nil, "Annullato."},
		{"change.no_right", nil, "Solo il proprietario può confermare."},
		{"change.saved", []any{"summary", "Preferisco il caffè", "minutes", 10}, "Salvato: Preferisco il caffè. Rispondi \"annulla modifica\" entro 10 minuti per tornare indietro."},
		{"file.send_too_big", []any{"name", "report.pdf", "limit", "20 MiB (limite della piattaforma)"}, "report.pdf è troppo grande per essere inviato qui (il limite è 20 MiB (limite della piattaforma)). Si trova nei file della chat."},
		{"acts.read_only", []any{"next", "Puoi cambiarlo più tardi nella pagina Trigger."}, "Permessi: sola lettura. Un'esecuzione può leggere e riferire, ma non può cambiare nulla. Puoi cambiarlo più tardi nella pagina Trigger."},
	} {
		if got := it.T(c.key, c.args...); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.key, got, c.want)
		}
	}
}

func TestEveryLanguageSpeaksTheWordsItAsksFor(t *testing.T) {
	// The text of a proposal asks for a word. The parser of the host reads the
	// same word: these are the words each language puts in quotes.
	for lang, word := range map[string]string{"it": "sì", "de": "ja", "es": "sí", "fr": "oui", "pt-BR": "sim", "en": "yes"} {
		if got := hostmsg.Template(lang, "change.ask"); !strings.Contains(got, word) {
			t.Errorf("%s: change.ask does not ask for %q: %s", lang, word, got)
		}
	}
}
