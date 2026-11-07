// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Resources named by their service", func() {
	bg := context.Background()

	setup := func(mode Mode) *fixture {
		f := newFixture(mode)
		Expect(f.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
		return f
	}

	It("keeps one record for each name of an owner and finds it from either side", func() {
		f := setup(ModeNamedUser)
		alice, bob := named("alice"), named("bob")
		first, err := f.policy.EnsureRef(bg, alice, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		again, err := f.policy.EnsureRef(bg, alice, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(first), "naming the same resource twice is one record")
		other, err := f.policy.EnsureRef(bg, bob, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(other.ResourceID).NotTo(Equal(first.ResourceID), "the same name of another owner is another resource")

		ref, ok := f.policy.FindRef(boardKind, "alice", "notes")
		Expect(ok).To(BeTrue())
		Expect(ref).To(Equal(first))
		_, ok = f.policy.FindRef(boardKind, "alice", "missing")
		Expect(ok).To(BeFalse())
		_, ok = f.policy.FindRef("board:other", "alice", "notes")
		Expect(ok).To(BeFalse())

		owner, local, ok := f.policy.LocalOf(first)
		Expect(ok).To(BeTrue())
		Expect(owner).To(Equal("alice"))
		Expect(local).To(Equal("notes"))
		_, _, ok = f.policy.LocalOf(Ref{Kind: "board:other", ResourceID: first.ResourceID})
		Expect(ok).To(BeFalse(), "a record is found only under its own kind")

		// A grant works on the named record like on any other.
		Expect(f.policy.SetGrantRef(bg, alice, first, "bob", RoleUse)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, bob, first, ActionRead)).To(Succeed())

		// The name survives a restart of the store.
		Expect(f.store.Close()).To(Succeed())
		g := open(ModeNamedUser, f.dir)
		Expect(g.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
		ref, ok = g.policy.FindRef(boardKind, "alice", "notes")
		Expect(ok).To(BeTrue())
		Expect(ref).To(Equal(first))
	})

	It("refuses a name that is empty, too long or has control characters, an unknown kind and local-owner mode", func() {
		f := setup(ModeNamedUser)
		alice := named("alice")
		for _, bad := range []string{"", strings.Repeat("x", 257), "a\nb", "a\x00b"} {
			_, err := f.policy.EnsureRef(bg, alice, boardKind, bad)
			Expect(err).To(MatchError(ErrForbidden), "name %q", bad)
		}
		_, err := f.policy.EnsureRef(bg, alice, "board:unknown", "notes")
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.policy.EnsureRef(bg, alice, addonKind("fixture"), "notes")
		Expect(err).To(MatchError(ErrForbidden))
		f.accounts.blocked["alice"] = true
		_, err = f.policy.EnsureRef(bg, alice, boardKind, "notes")
		Expect(err).To(MatchError(ErrForbidden), "an account that is not active names nothing")

		local := setup(ModeLocalOwner)
		_, err = local.policy.EnsureRef(bg, contract.Actor{UserID: LocalOwnerID, CredentialID: LocalOwnerID, Mode: "local-owner"}, boardKind, "notes")
		Expect(err).To(MatchError(ErrForbidden), "local-owner mode has nobody to share with")
	})

	It("forgets a record for its owner only, after the work of every grantee stopped", func() {
		f := setup(ModeNamedUser)
		alice, bob := named("alice"), named("bob")
		ref, err := f.policy.EnsureRef(bg, alice, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, alice, ref, "bob", RoleUse)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, alice, ref, Everyone, RoleUse)).To(Succeed())
		work := runHeld(f, bob, ref, ActionRead)

		Expect(f.policy.ForgetRef(bg, bob, ref)).To(MatchError(ErrForbidden), "a grantee cannot forget the record")
		Expect(work.cancelled()).To(BeFalse())

		Expect(f.policy.ForgetRef(bg, alice, ref)).To(Succeed())
		Expect(work.cancelled()).To(BeTrue(), "the grantee's work stops before the record goes")
		work.finish()
		_, ok := f.policy.FindRef(boardKind, "alice", "notes")
		Expect(ok).To(BeFalse())
		_, _, ok = f.policy.LocalOf(ref)
		Expect(ok).To(BeFalse())
		Expect(f.policy.AuthorizeRef(bg, bob, ref, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.ForgetRef(bg, alice, ref)).To(MatchError(ErrForbidden), "a record that is gone is the same denial")

		// The name is free again and a new record starts with no grant.
		fresh, err := f.policy.EnsureRef(bg, alice, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh.ResourceID).NotTo(Equal(ref.ResourceID))
		Expect(f.policy.AuthorizeRef(bg, bob, fresh, ActionRead)).To(MatchError(ErrForbidden))
	})
})
