package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// These rows are the contract between the carriers and the in-memory doubles
// that specs publish through. When each had its own copy of the rule, a filter
// could match on one and not on the other, and the difference looked like a peer
// that receives an event on one replica and misses it on another.
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

	// A model id that looks like several tokens is one token after the
	// sanitizer, so the staging filter can be a single '*'.
	Entry("the staging wildcard matches a sanitized multi-part model id",
		messaging.SubjectStagingProgressWildcard, messaging.SubjectStagingProgress("a.b.c"), true),

	// '>' is not implemented. A caller that writes one must get nothing, and
	// not every message on the prefix.
	Entry("a tail wildcard filter matches nothing", "a.>", "a.b", false),
	Entry("a tail wildcard filter matches nothing even one token deep", "a.>", "a.b.c", false),
	Entry("a bare tail wildcard matches nothing", ">", "anything", false),
	// The refusal comes before the check for equal strings. Without that order
	// the filter would match the literal subject "a.>".
	Entry("a tail wildcard filter does not even match itself", "a.>", "a.>", false),
)
