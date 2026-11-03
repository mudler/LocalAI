// SPDX-License-Identifier: MIT
package gateway

import (
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// refreshSpy records the calls of the hook that asks the manager to look again
// at what each add-on holds.
type refreshSpy struct {
	mu    sync.Mutex
	calls int
}

func (s *refreshSpy) hook(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
}

func (s *refreshSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

var _ = Describe("a grant change that takes access away", func() {
	var spy *refreshSpy
	newFixture := func() *hgFixture {
		spy = &refreshSpy{}
		return newHGFixture(func(c *HostGrantsConfig) { c.AfterChange = spy.hook })
	}
	edit := func(f *hgFixture, op string, rev uint64, ops ...string) {
		GinkgoHelper()
		_, err := f.svc.Serve(human, admin, call(HostGrantsEdit, op, "sandbox", HostGrantsEditRequest{OperationID: op, Revision: rev, Ops: ops}))
		Expect(err).NotTo(HaveOccurred())
	}

	It("asks the manager to re-sync after an edit that only removes", func() {
		f := newFixture()
		edit(f, "e1", 0, "workspace.materialize", "egress.check")
		before := spy.count()
		edit(f, "e2", 1, "egress.check")
		Expect(spy.count()).To(Equal(before + 1))
	})

	It("asks the manager to re-sync after a revoke", func() {
		f := newFixture()
		edit(f, "e1", 0, "workspace.materialize")
		before := spy.count()
		_, err := f.svc.Serve(human, admin, call(HostGrantsRevoke, "r1", "sandbox", HostGrantsRevokeRequest{OperationID: "r1", Revision: 1}))
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.count()).To(Equal(before + 1))
	})

	It("asks the manager to re-sync after a deny is added and after it is removed", func() {
		f := newFixture()
		edit(f, "e1", 0, "workspace.materialize")
		before := spy.count()
		deny := func(op string, rev uint64, rules map[string][]string) {
			_, err := f.svc.Serve(human, admin, call(HostGrantsDeny, op, "", HostGrantsDenyRequest{OperationID: op, Revision: rev, Deny: rules}))
			Expect(err).NotTo(HaveOccurred())
		}
		deny("d1", 1, map[string][]string{"sandbox": {"workspace."}})
		Expect(spy.count()).To(Equal(before + 1))
		deny("d2", 2, map[string][]string{})
		Expect(spy.count()).To(Equal(before+2), "an add-on refused for the deny must get another try")
	})

	It("asks the manager to re-sync when the blanket grants are revoked", func() {
		f := newFixture()
		b := func(op string, rev uint64, r HostGrantsBlanketRequest) {
			GinkgoHelper()
			r.OperationID, r.Revision = op, rev
			_, err := f.svc.Serve(human, admin, call(HostGrantsBlanket, op, "", r))
			Expect(err).NotTo(HaveOccurred())
		}
		b("b1", 0, HostGrantsBlanketRequest{Mode: "any", ApplyToInstalled: true, Confirm: BlanketConfirmAny})
		before := spy.count()
		b("b2", 1, HostGrantsBlanketRequest{Mode: "off", RevokeOnOff: true})
		Expect(spy.count()).To(Equal(before + 1))
	})

	It("does not ask again for a replayed operation", func() {
		f := newFixture()
		edit(f, "e1", 0, "workspace.materialize")
		n := spy.count()
		edit(f, "e1", 0, "workspace.materialize")
		Expect(spy.count()).To(Equal(n))
	})
})

var _ = Describe("a grant write whose audit line cannot be written", func() {
	var f *hgFixture
	var audit *hostgrants.Audit
	BeforeEach(func() {
		var err error
		audit, err = hostgrants.OpenAudit(filepath.Join(GinkgoT().TempDir(), "audit.jsonl"))
		Expect(err).NotTo(HaveOccurred())
		f = newHGFixture(func(c *HostGrantsConfig) { c.Audit = audit })
	})
	revision := func() uint64 {
		snap, err := f.store.Snapshot(human)
		Expect(err).NotTo(HaveOccurred())
		return snap.Revision
	}
	// breakAudit makes every later append fail, as a full disk would.
	breakAudit := func() { Expect(audit.Close()).To(Succeed()) }

	It("fails closed with the unavailable answer and leaves the document unchanged", func() {
		_, err := f.svc.Serve(human, admin, call(HostGrantsEdit, "e1", "sandbox", HostGrantsEditRequest{OperationID: "e1", Ops: []string{"workspace.materialize"}}))
		Expect(err).NotTo(HaveOccurred())
		Expect(revision()).To(Equal(uint64(1)))
		breakAudit()
		for _, c := range []HostGrantsCall{
			call(HostGrantsEdit, "e2", "sandbox", HostGrantsEditRequest{OperationID: "e2", Revision: 1, Ops: []string{"workspace.materialize", "egress.check"}}),
			call(HostGrantsRevoke, "r1", "sandbox", HostGrantsRevokeRequest{OperationID: "r1", Revision: 1}),
			call(HostGrantsDeny, "d1", "", HostGrantsDenyRequest{OperationID: "d1", Revision: 1, Deny: map[string][]string{"sandbox": {"egress."}}}),
			call(HostGrantsBlanket, "b1", "", HostGrantsBlanketRequest{OperationID: "b1", Revision: 1, Mode: "any", ApplyToInstalled: true, Confirm: BlanketConfirmAny}),
		} {
			_, err := f.svc.Serve(human, admin, c)
			Expect(err).To(MatchError(ErrUnavailable), c.Op)
			Expect(revision()).To(Equal(uint64(1)), c.Op+" must not change the document")
		}
		snap, err := f.store.Snapshot(human)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Grants["sandbox"].Ops).To(HaveLen(1))
		Expect(snap.Deny).To(BeEmpty())
		Expect(snap.Blanket.Mode).To(Equal(hostgrants.BlanketOff))
	})
})
