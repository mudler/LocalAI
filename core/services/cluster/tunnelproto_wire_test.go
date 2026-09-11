// SPDX-License-Identifier: MIT

package cluster

// In-package, and that is the point: these specs assert the BYTES a refusal
// puts on the wire, against literals written out here rather than against the
// constants the code uses. A spec that round-trips through this process's own
// writer and reader cannot see a rename, because a rename moves both sides at
// once; the DescribeTable in tunnelproto_test.go is exactly that spec and it
// stays green through any renaming of the four codes.
//
// A wire code is a cross-version contract. A worker and a frontend built from
// different commits talk to each other over it, and the consequence of them
// disagreeing is not a parse error: an unrecognised code is deliberately
// treated as "not the worker's answer", so renaming `unavailable` would turn
// every crashed backend on a tunnelled worker into a row nothing can ever reap,
// silently and with the whole suite green. That is the exact defect this phase
// spent two rounds removing.
//
// This branch set the precedent for pinning a vocabulary against literals in
// core/services/messaging/subjects_wire_test.go, for the same reason. Nothing
// has shipped yet, so no value here is load bearing across a release boundary
// today; that is why the literals may still be changed, and why the change has
// to be deliberate rather than incidental.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// frameBytes returns the payload of the single frame w wrote, so a spec can
// assert on the bytes rather than on what the reader makes of them.
func frameBytes(write func(w *bytes.Buffer) error) string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(write(&buf)).To(Succeed())
	raw := buf.Bytes()
	Expect(len(raw)).To(BeNumerically(">=", 2))
	Expect(binary.BigEndian.Uint16(raw[:2])).To(Equal(uint16(len(raw) - 2)))
	return string(raw[2:])
}

var _ = Describe("the tunnel refusal vocabulary on the wire", func() {
	DescribeTable("writes the exact code an older build reads",
		func(sentinel error, wantCode string) {
			payload := frameBytes(func(w *bytes.Buffer) error {
				return WriteStreamRefusal(w, fmt.Errorf("%w: because", sentinel))
			})
			Expect(payload).To(Equal("err " + wantCode + " " + sentinel.Error() + ": because"))
		},
		Entry("unknown tag", ErrStreamTagUnknown, "unknown-tag"),
		Entry("unavailable target", ErrStreamTargetUnavailable, "unavailable"),
		Entry("invalid request", ErrStreamRequestInvalid, "bad-request"),
		Entry("nothing learned", ErrStreamNotServed, "not-served"),
	)

	DescribeTable("reads the exact code an older build writes",
		func(rawCode string, want error) {
			var buf bytes.Buffer
			Expect(writeFrame(&buf, "err "+rawCode+" some reason")).To(Succeed())
			Expect(ReadStreamReply(&buf)).To(MatchError(want))
		},
		Entry("unknown tag", "unknown-tag", ErrStreamTagUnknown),
		Entry("unavailable target", "unavailable", ErrStreamTargetUnavailable),
		Entry("invalid request", "bad-request", ErrStreamRequestInvalid),
		Entry("nothing learned", "not-served", ErrStreamNotServed),
	)

	It("accepts a stream with the literal an older build sends", func() {
		// The success case has a literal too, and a rename of it would refuse
		// every stream rather than mis-classify one, which is at least loud.
		Expect(frameBytes(func(w *bytes.Buffer) error { return WriteStreamAccepted(w) })).To(Equal("ok"))
	})

	It("names the two stream tags with the literals the worker routes on", func() {
		// The worker's routing table is keyed by these, so a rename here is a
		// worker that serves nothing while reporting an unknown tag, which is a
		// verdict a frontend acts on.
		Expect(StreamTagGRPC).To(Equal("grpc"))
		Expect(StreamTagHTTP).To(Equal("http"))
	})

	It("pins which codes a frontend may act on as evidence about a backend", func() {
		// The half of the table that decides whether a row is deleted. It is
		// asserted against the literals, not against IsWorkerAnswer's own
		// output, so moving a code between the two columns reddens here as well
		// as at the consumer.
		evidence := map[string]bool{}
		for _, r := range streamRefusals {
			evidence[r.code] = r.evidence
		}
		Expect(evidence).To(Equal(map[string]bool{
			"unknown-tag": true,
			"unavailable": true,
			"bad-request": true,
			"not-served":  false,
		}), "a code that changed column changes whether a live model gets evicted")
	})

	It("has exactly one entry per sentinel, and no duplicate codes", func() {
		// A duplicate code makes the reader's first match win and the writer's
		// first match win, which need not be the same entry.
		codes := map[string]int{}
		for _, r := range streamRefusals {
			Expect(r.sentinel).ToNot(BeNil())
			codes[r.code]++
		}
		Expect(codes).To(HaveLen(len(streamRefusals)))
		for code, n := range codes {
			Expect(n).To(Equal(1), "code %q appears %d times", code, n)
		}
	})

	It("classifies every sentinel as a refusal, and nothing else as one", func() {
		// IsStreamRefusal is what stops the worker re-classifying a decision
		// something closer to the failure already made. Derived from the table
		// so a fifth code joins it automatically; asserted here so that
		// derivation cannot quietly stop.
		for _, r := range streamRefusals {
			Expect(IsStreamRefusal(fmt.Errorf("wrapped: %w", r.sentinel))).To(BeTrue(), r.code)
		}
		Expect(IsStreamRefusal(errors.New("a plain failure"))).To(BeFalse())
		Expect(IsStreamRefusal(nil)).To(BeFalse())
	})
})
