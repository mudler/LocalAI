package messaging

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Every carrier serves the same closed set of subject roots. A subject outside
// it is refused at publish and at subscribe instead of being carried, because a
// subject that one carrier accepts and another drops is a message that is
// delivered to nobody, with no error anywhere. NATS would accept it, so the rule
// has to be stated here, once, rather than left to whichever carrier is in use.
var (
	// broadcastRoots carry fan-out and the competing-consumer subjects.
	broadcastRoots = map[string]struct{}{
		"jobs": {}, "agent": {}, "gallery": {}, "cache": {},
		"staging": {}, "prefixcache": {}, "responses": {}, "state": {},
		"finetune": {},
	}
	// controlRoots carry request/reply to one node or one agent worker.
	controlRoots = map[string]struct{}{
		"nodes": {}, "mcp": {},
	}
)

// ErrUnservedSubject is the class every root refusal belongs to, so a caller can
// tell "this carrier does not serve that family" from a transport failure
// without matching on strings.
var ErrUnservedSubject = errors.New("messaging: subject root is not served")

// ErrUnsupportedWildcard reports a wildcard other than a whole single token.
// Only `*` standing alone in a non-root position is part of the contract; `>` is
// not, because not every carrier can honour it.
var ErrUnsupportedWildcard = errors.New("messaging: unsupported wildcard in subject")

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BroadcastRoots returns the fan-out and queue roots, sorted. Exported so a spec
// can assert over the whole set instead of a sample it re-spells.
func BroadcastRoots() []string { return sortedKeys(broadcastRoots) }

// ControlRoots returns the request/reply roots, sorted.
func ControlRoots() []string { return sortedKeys(controlRoots) }

// SubjectRoot returns the first token of a subject.
func SubjectRoot(subject string) string {
	root, _, _ := strings.Cut(subject, ".")
	return root
}

// ValidateSubject reports whether a subject, or a subscription filter, is one
// every carrier serves.
func ValidateSubject(subject string) error {
	if subject == "" {
		return fmt.Errorf("%w: empty subject", ErrUnservedSubject)
	}
	tokens := strings.Split(subject, ".")
	for i, tok := range tokens {
		switch {
		case tok == "":
			return fmt.Errorf("%w: %q has an empty token", ErrUnservedSubject, subject)
		case strings.Contains(tok, ">"):
			return fmt.Errorf("%w: %q", ErrUnsupportedWildcard, subject)
		case strings.Contains(tok, "*") && tok != "*":
			return fmt.Errorf("%w: %q", ErrUnsupportedWildcard, subject)
		case tok == "*" && i == 0:
			return fmt.Errorf("%w: %q has a wildcard root", ErrUnsupportedWildcard, subject)
		}
	}
	root := tokens[0]
	if _, ok := broadcastRoots[root]; ok {
		return nil
	}
	if _, ok := controlRoots[root]; ok {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnservedSubject, subject)
}

// MatchSubject reports whether a subscription filter matches a concrete subject,
// honouring the single-token `*` wildcard. It is the one spelling of the
// matching rule: a carrier that delivers on its own reading of it would make the
// same subscription behave differently per mode.
func MatchSubject(filter, subject string) bool {
	if filter == subject {
		return true
	}
	fp := strings.Split(filter, ".")
	sp := strings.Split(subject, ".")
	if len(fp) != len(sp) {
		return false
	}
	for i := range fp {
		if fp[i] == "*" {
			continue
		}
		if fp[i] != sp[i] {
			return false
		}
	}
	return true
}
