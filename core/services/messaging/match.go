package messaging

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedFilter is the class every ValidFilter rejection belongs to, so a
// carrier can tell "this caller asked for something we do not implement" apart
// from "the store is unhappy" without string matching.
var ErrUnsupportedFilter = errors.New("unsupported subject filter")

// SubjectMatches reports whether a subscription filter matches a concrete
// subject, honoring the single-token `*` wildcard used by NATS.
//
// This is the single definition of subject matching for the whole tree: the
// carrier that delivers messages in production and the in-memory doubles the
// specs publish through both call it. When those were separate copies, a filter
// could match on one and not the other, and the difference read as a peer that
// received an event on one replica and missed it on another.
//
// A `>` tail wildcard is deliberately NOT implemented and makes the filter
// match nothing, so a caller who writes one receives no messages rather than
// silently receiving every message on the prefix. The refusal is checked before
// the exact-equality fast path, otherwise `a.>` would still match the literal
// subject `a.>`.
func SubjectMatches(filter, subject string) bool {
	// sanitizeSubjectToken folds '>' out of every generated token, so a '>'
	// anywhere in a filter can only be a hand-written tail wildcard.
	if strings.Contains(filter, ">") {
		return false
	}
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

// ValidFilter reports whether a filter is one SubjectMatches can act on, so a
// subscriber is refused at subscribe time instead of staying silently empty for
// the life of the process. It rejects an empty filter, a filter containing the
// unimplemented `>` tail wildcard, and a filter with an empty token (`a..b`),
// which no subject generator can ever produce.
func ValidFilter(filter string) error {
	if filter == "" {
		return fmt.Errorf("%w: filter is empty", ErrUnsupportedFilter)
	}
	if strings.Contains(filter, ">") {
		return fmt.Errorf("%w: %q uses the tail wildcard '>', which is not implemented", ErrUnsupportedFilter, filter)
	}
	for _, token := range strings.Split(filter, ".") {
		if token == "" {
			return fmt.Errorf("%w: %q has an empty token", ErrUnsupportedFilter, filter)
		}
	}
	return nil
}
