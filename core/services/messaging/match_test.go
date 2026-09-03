package messaging_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// These rows are the shared contract between the PostgreSQL-backed carrier and
// the in-memory doubles. Both spellings of "does this filter match this
// subject" used to be copied by hand, and a drift between them reads as a peer
// that receives an event on one replica and not on another.
var _ = DescribeTable("SubjectMatches",
	func(filter, subject string, expected bool) {
		Expect(messaging.SubjectMatches(filter, subject)).To(Equal(expected))
	},
	Entry("an exact subject matches itself", "jobs.new", "jobs.new", true),
	Entry("a different literal does not match", "jobs.new", "jobs.done", false),
	Entry("matching is case sensitive", "Jobs.new", "jobs.new", false),

	Entry("two wildcards each consume one token",
		"agent.*.events.*", "agent.myagent.events.user1", true),
	Entry("a wildcard does not match a missing token",
		"agent.*.events.*", "agent.myagent.events", false),
	Entry("a wildcard does not swallow two tokens",
		"agent.*.events.*", "agent.a.b.events.u", false),
	Entry("a wildcard does not match a longer subject",
		"agent.*.events.*", "agent.myagent.events.user1.extra", false),

	Entry("a middle wildcard matches one token",
		"jobs.*.progress", "jobs.abc.progress", true),
	Entry("a middle wildcard does not match two tokens",
		"jobs.*.progress", "jobs.abc.def.progress", false),
	Entry("a middle wildcard does not match zero tokens",
		"jobs.*.progress", "jobs.progress", false),

	Entry("a bare wildcard matches exactly one token", "*", "jobs", true),
	Entry("a bare wildcard does not match two tokens", "*", "jobs.new", false),

	// The sanitizer folds '.', '*' and '>' out of an id, so a model id that
	// looks like several tokens still occupies exactly one, which is the only
	// reason the staging wildcard can be a single '*'.
	Entry("the staging wildcard matches a sanitized multi-part model id",
		messaging.SubjectStagingProgressWildcard, messaging.SubjectStagingProgress("a.b.c"), true),

	// '>' is refused rather than implemented: no surviving subscription uses
	// it, and a caller who writes one must get nothing rather than everything.
	Entry("a tail wildcard filter matches nothing", "a.>", "a.b", false),
	Entry("a tail wildcard filter matches nothing even one token deep", "a.>", "a.b.c", false),
	Entry("a bare tail wildcard matches nothing", ">", "anything", false),
	// This row is the one that proves the '>' refusal is checked BEFORE the
	// exact-equality fast path, which is where a naive implementation leaks.
	Entry("a tail wildcard filter does not even match itself", "a.>", "a.>", false),
)

var _ = DescribeTable("ValidFilter",
	func(filter string, wantErr bool) {
		err := messaging.ValidFilter(filter)
		if wantErr {
			// The CLASS, not merely "an error". Carriers match on
			// ErrUnsupportedFilter to tell "this caller asked for something we
			// do not implement" apart from "the store is unhappy", so the
			// definition has to pin what the callers match on.
			Expect(errors.Is(err, messaging.ErrUnsupportedFilter)).To(BeTrue(), "got %v", err)
			return
		}
		Expect(err).ToNot(HaveOccurred())
	},
	Entry("an empty filter is refused", "", true),
	Entry("a tail wildcard is refused", "a.>", true),
	Entry("a bare tail wildcard is refused", ">", true),
	Entry("an embedded tail wildcard is refused", "a.>.b", true),
	Entry("an empty middle token is refused", "a..b", true),
	Entry("an empty leading token is refused", ".a", true),
	Entry("an empty trailing token is refused", "a.", true),
	Entry("a single-token wildcard is accepted", "a.*.b", false),
	Entry("a literal filter is accepted", "a.b", false),
	Entry("a bare wildcard is accepted", "*", false),
)
