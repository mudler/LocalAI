package studio_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The words that answer a proposal are read in every language the host speaks.
// The host asks with these words, so each must be understood when it comes back.
var _ = Describe("the short answers to a proposal", func() {
	yes := map[string][]string{
		"en":    {"yes", "ok", "confirm"},
		"it":    {"sì", "si", "certo", "conferma", "va bene", "confermo"},
		"de":    {"ja", "klar", "bestätigen", "einverstanden"},
		"es":    {"sí", "claro", "vale", "confirmar", "de acuerdo"},
		"fr":    {"oui", "d'accord", "confirmer", "bien sûr"},
		"pt-BR": {"sim", "pode", "confirmar", "beleza"},
	}
	no := map[string][]string{
		"en":    {"no", "cancel", "stop"},
		"it":    {"no", "annulla", "annullare", "no grazie"},
		"de":    {"nein", "abbrechen", "stopp"},
		"es":    {"no", "cancelar", "no gracias"},
		"fr":    {"non", "annuler", "non merci"},
		"pt-BR": {"não", "cancelar", "nao"},
	}
	undo := []string{"undo", "annulla modifica", "rückgängig", "deshacer", "annuler la modification", "desfazer"}

	It("understands a yes in each language", func() {
		for lang, words := range yes {
			for _, w := range words {
				verb, code, ok := studio.ParseReply(w)
				Expect(ok).To(BeTrue(), "%s: %q", lang, w)
				Expect(verb).To(Equal("confirm"), "%s: %q", lang, w)
				Expect(code).To(BeEmpty())
				verb, code, ok = studio.ParseReply(w + " 7k2q")
				Expect([]any{verb, code, ok}).To(Equal([]any{"confirm", "7K2Q", true}), "%s: %q with a code", lang, w)
			}
		}
	})

	It("understands a no in each language", func() {
		for lang, words := range no {
			for _, w := range words {
				verb, _, ok := studio.ParseReply(w + ".")
				Expect(ok).To(BeTrue(), "%s: %q", lang, w)
				Expect(verb).To(Equal("cancel"), "%s: %q", lang, w)
			}
		}
	})

	It("understands the word that takes a change back, and the phrase the host asks for", func() {
		for _, w := range undo {
			verb, _, ok := studio.ParseReply(w)
			Expect(ok).To(BeTrue(), "%q", w)
			Expect(verb).To(Equal("cancel"))
		}
	})

	It("reads the verbs that the host teaches in its own text", func() {
		// The text asks "confirmar CODIGO", "bestätigen CODE" and so on: each
		// verb of the catalog must come back as an answer.
		for _, lang := range hostmsg.Languages {
			for _, key := range []string{"change.ask", "trigger.proposal", "trigger.auto", "proposal.multi"} {
				text := hostmsg.Template(lang, key)
				for _, verb := range quotedWords(text) {
					if verb == "" {
						continue
					}
					_, _, ok := studio.ParseReply(verb)
					Expect(ok).To(BeTrue(), "%s %s asks for %q, which the host does not read", lang, key, verb)
				}
			}
		}
	})

	It("does not take a sentence for an answer", func() {
		for _, s := range []string{"sì ma prima dimmi il costo", "can you do it tomorrow", "nein danke, später vielleicht", "no sé", "oui mais plus tard"} {
			_, _, ok := studio.ParseReply(s)
			Expect(ok).To(BeFalse(), "%q", s)
		}
	})
})

// quotedWords lists the words between double quotes in a template, without a
// code placeholder or the capital word that stands for a code.
func quotedWords(tpl string) []string {
	tpl = strings.NewReplacer("« ", "\"", " »", "\"").Replace(tpl)
	var out []string
	for {
		i := strings.IndexByte(tpl, '"')
		if i < 0 {
			return out
		}
		rest := tpl[i+1:]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			return out
		}
		w := rest[:j]
		tpl = rest[j+1:]
		// "confirm {code}" and "confirm CODE": keep the verb only.
		if k := strings.IndexByte(w, ' '); k >= 0 {
			w = w[:k]
		}
		out = append(out, w)
	}
}
