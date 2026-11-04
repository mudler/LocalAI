package studio

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The words a person answers a proposal with. The lists are short on purpose:
// a message is an answer only when all of it is one of these (with an
// optional code), so a longer message always goes to the agent. Words of other
// languages are optional; these cover the common ones.
var (
	yesWords = wordSet("yes", "y", "yep", "yeah", "ok", "okay", "sure", "confirm", "approve", "approved", "go ahead", "do it",
		"👍", "✅", "si", "sì", "ja", "oui", "vale", "ok grazie")
	noWords = wordSet("no", "n", "nope", "cancel", "stop", "drop it", "no thanks", "no thank you", "never mind", "nevermind", "undo",
		"👎", "❌", "nein", "non", "annulla")
)

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
	case noWords[t]:
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
