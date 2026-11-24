package studio

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The words a person answers a proposal with. The lists are short on purpose:
// a message is an answer only when all of it is one of these (with an
// optional code), so a longer message always goes to the agent. Words of other
// languages are optional; these cover the common ones.
var (
	yesWords = wordSet(slices.Concat(
		[]string{"yes", "y", "yep", "yeah", "ok", "okay", "sure", "confirm", "approve", "approved", "go ahead", "do it", "👍", "✅"},
		// Italian
		[]string{"si", "sì", "certo", "certamente", "conferma", "confermo", "confermato", "va bene", "d'accordo", "daccordo", "perfetto", "procedi", "salva", "avvia", "ok grazie", "ok va bene"},
		// German
		[]string{"ja", "jawohl", "klar", "natürlich", "bestätigen", "bestätige", "bestätigt", "einverstanden", "passt", "mach das", "speichern", "starten", "ok danke"},
		// Spanish
		[]string{"sí", "claro", "vale", "confirmar", "confirmo", "confirma", "de acuerdo", "adelante", "hazlo", "guardar", "guárdalo", "ok gracias"},
		// French
		[]string{"oui", "ouais", "d'accord", "daccord", "bien sûr", "confirmer", "confirme", "je confirme", "vas-y", "enregistre", "enregistrer", "ok merci"},
		// Portuguese (Brazil)
		[]string{"sim", "pode", "pode salvar", "beleza", "certo", "confirmar", "confirma", "de acordo", "bora", "salvar", "ok obrigado", "ok obrigada"},
	)...)
	noWords = wordSet(slices.Concat(
		[]string{"no", "n", "nope", "cancel", "stop", "drop it", "no thanks", "no thank you", "never mind", "nevermind", "undo", "👎", "❌"},
		// Italian
		[]string{"annulla", "annullare", "annullato", "lascia stare", "no grazie", "scarta", "ferma"},
		// German
		[]string{"nein", "nö", "abbrechen", "abbruch", "stopp", "lass es", "lass gut sein", "nein danke", "verwerfen"},
		// Spanish
		[]string{"cancelar", "cancela", "déjalo", "olvídalo", "no gracias", "descartar"},
		// French
		[]string{"non", "annuler", "annule", "laisse tomber", "non merci", "oublie", "abandonner"},
		// Portuguese (Brazil)
		[]string{"não", "nao", "cancelar", "cancela", "deixa pra lá", "não obrigado", "nao obrigado", "descartar", "esquece"},
	)...)

	// undoWords take back a change that was just applied from the chat. A
	// bare "no" or "cancel" after a save does not: it may mean anything. Each
	// language has its own phrase, the one its confirmation text asks for.
	undoWords = wordSet("undo", "annulla modifica", "annulla la modifica", "rückgängig", "rückgängig machen", "deshacer",
		"annuler la modification", "défaire", "desfazer")
)

// isUndoReply says whether the whole message is the word that takes back the
// change that was just applied.
func isUndoReply(text string) bool { return undoWords[normalizeReply(text)] }

func wordSet(w ...string) map[string]bool {
	m := make(map[string]bool, len(w))
	for _, x := range w {
		m[x] = true
	}
	return m
}

var replyCodeRE = regexp.MustCompile(`^(.+?)\s+([a-z0-9]{4})$`)

// normalizeReply lowercases a message and removes the space and the sentence
// marks around it.
func normalizeReply(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.Trim(text, " \t.!?,;:")
	return strings.Join(strings.Fields(text), " ")
}

// ParseReply reads a short answer to a proposal: a word of agreement or
// refusal, alone ("yes", "ok", "cancel") or followed by the code of the
// proposal ("yes 7K2Q"). The verb is "confirm" or "cancel"; the code is in upper
// case, or empty for a bare word. Any other text is not an answer.
func ParseReply(text string) (verb, code string, ok bool) {
	t := normalizeReply(text)
	if t == "" || len(t) > 40 {
		return "", "", false
	}
	if v, found := replyVerb(t); found {
		return v, "", true
	}
	if m := replyCodeRE.FindStringSubmatch(t); m != nil {
		if v, found := replyVerb(m[1]); found {
			return v, strings.ToUpper(m[2]), true
		}
	}
	return "", "", false
}

func replyVerb(t string) (string, bool) {
	switch {
	case yesWords[t]:
		return "confirm", true
	case noWords[t], undoWords[t]:
		return "cancel", true
	}
	return "", false
}

// ---- Times that a chat gives ----

var (
	delayPartRE  = regexp.MustCompile(`(\d+)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m)`)
	delayWholeRE = regexp.MustCompile(`^(\d+\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m)\s*)+$`)
)

// ParseDelay reads how long to wait: "10m", "10 minutes", "an hour", "1h30m",
// "2 days", "half an hour". The unit is needed, and the result is positive.
func ParseDelay(text string) (time.Duration, error) {
	t := normalizeReply(text)
	t = strings.NewReplacer("half an hour", "30 minutes", "an hour", "1 hour", "a minute", "1 minute", "a day", "1 day").Replace(t)
	if t == "" || !delayWholeRE.MatchString(t) {
		return 0, errors.New(`the delay must look like "10 minutes", "1h30m" or "2 days"`)
	}
	var total time.Duration
	for _, m := range delayPartRE.FindAllStringSubmatch(t, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n > 10000 {
			return 0, errors.New("the delay is too large")
		}
		var unit time.Duration
		switch m[2][0] {
		case 'd':
			unit = 24 * time.Hour
		case 'h':
			unit = time.Hour
		default:
			unit = time.Minute
		}
		total += time.Duration(n) * unit
	}
	if total <= 0 {
		return 0, errors.New("the delay must be more than zero")
	}
	return total, nil
}
