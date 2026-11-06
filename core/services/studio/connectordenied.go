package studio

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The note is cut at 400 characters by the session, so it names the reasons
// and not every tool, stays short, and goes first in the note of the turn.
const (
	deniedNoteStart  = "Held back from this sender: "
	deniedNoteEnd    = " If asked for one of these, relay the reason plainly; never say it does not exist."
	deniedNoteBudget = 400 - len(deniedNoteStart) - len(deniedNoteEnd) - 1
)

// deniedToolsNote tells the model which tools of this turn the sender may not
// use and why, so that a request for one gets an answer the person can act on
// ("the owner has not enabled reminders for this bot") and not "I have no such
// tool". It is empty for a chat that no connector started, and when nothing was
// held back for a missing right.
func deniedToolsNote(rec session.ChatRecord, built []builtins.Built) string {
	if rec.Origin != session.OriginConnector {
		return ""
	}
	prefix := builtins.ReasonNotPermitted + ": "
	var words []string
	for name, w := range capabilityWords {
		words = append(words, name, w.Noun)
	}
	say := strings.NewReplacer(words...)
	var reasons []string
	for _, b := range built {
		why, ok := strings.CutPrefix(b.Reason, prefix)
		if b.OK || !ok || why == "" {
			continue
		}
		if why = sentence(say.Replace(why)); !slices.Contains(reasons, why) {
			reasons = append(reasons, why)
		}
	}
	slices.Sort(reasons)
	// A tool of an add-on the agent was given is what the person asks for by
	// name, so its reason stays when the budget cuts the rest.
	slices.SortStableFunc(reasons, func(a, b string) int {
		return boolRank(strings.Contains(a, heldMark)) - boolRank(strings.Contains(b, heldMark))
	})
	var kept []string
	used := 0
	for _, why := range reasons {
		if used+len(why)+1 > deniedNoteBudget {
			break
		}
		kept = append(kept, why)
		used += len(why) + 1
	}
	if len(kept) == 0 {
		return ""
	}
	return deniedNoteStart + strings.Join(kept, " ") + deniedNoteEnd
}

// heldMark is in the reason of an add-on tool that was held back (see heldTools).
const heldMark = "(held back, "

func boolRank(held bool) int {
	if held {
		return 0
	}
	return 1
}

// sentence makes a reason a whole sentence: it starts with a capital and ends
// with a full stop. The reasons of the table already are; a reason that a tool
// wrote on its own may not be.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if r, n := utf8.DecodeRuneInString(s); unicode.IsLower(r) {
		s = string(unicode.ToUpper(r)) + s[n:]
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
