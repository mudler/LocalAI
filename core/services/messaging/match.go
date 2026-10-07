package messaging

import "strings"

// SubjectMatches reports whether a subscription filter matches a concrete
// subject. A `*` that stands alone as a token matches exactly one token, as in
// NATS.
//
// It is the one definition of subject matching in the tree. A carrier that
// matches subjects itself and the in-memory doubles that specs publish through
// both call it, so a filter cannot match on one and miss on the other.
//
// A `>` tail wildcard is not implemented, and a filter with one matches
// nothing. A caller that writes one then receives no message and not every
// message on the prefix. The check comes before the test for equal strings,
// because otherwise `a.>` would match the literal subject `a.>`.
func SubjectMatches(filter, subject string) bool {
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
