package cluster

// These specs are in-package because the property they pin is a property of
// the error TYPE, not of any call site. Asserting it only from outside would
// re-check the paths peerlink_test.go already drives, which leaves the type
// free to start leaking its cause the moment a new call site is added.
//
// The other direction of the rule (a node absent from the registry is not
// merely unreachable) is driven end to end by peerlink_test.go through the
// real Registry, so it is not restated here.

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Peer unreachability is not node absence", func() {
	It("stays a transport error even when its cause is an absence error", func() {
		// The dial path resolves the peer's address through the registry, so
		// an ErrInstanceNotFound is genuinely reachable as a dial cause (a row
		// deleted between the lookup and a retry, say). If the type let that
		// through, a peer that merely would not answer would read as an absent
		// node, and a replica acting on absence evicts healthy workers.
		err := unreachablePeer("peer-1", fmt.Errorf("resolving: %w", ErrInstanceNotFound))

		Expect(errors.Is(err, ErrPeerUnreachable)).To(BeTrue())
		Expect(errors.Is(err, ErrInstanceNotFound)).To(BeFalse(),
			"the unreachable error must not unwrap to its cause, or absence leaks through it")
	})

	It("keeps the cause legible in its message", func() {
		// Withholding the cause from errors.Is must not withhold it from a
		// human reading a log line.
		err := unreachablePeer("peer-1", errors.New("connection refused"))
		Expect(err.Error()).To(ContainSubstring("peer-1"))
		Expect(err.Error()).To(ContainSubstring("connection refused"))
	})
})
