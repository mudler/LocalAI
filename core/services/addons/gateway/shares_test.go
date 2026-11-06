// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shareRecorder is an owner-side ShareManager that records what it was asked
// and fails as told.
type shareRecorder struct {
	calls []string
	err   error
}

func (s *shareRecorder) Shares(_ context.Context, _ contract.Actor, kind string) (ShareIndex, error) {
	s.calls = append(s.calls, "list "+kind)
	return ShareIndex{Kind: kind}, s.err
}

func (s *shareRecorder) ShareGrants(_ context.Context, _ contract.Actor, kind, id string) (ShareList, error) {
	s.calls = append(s.calls, "grants "+kind+" "+id)
	return ShareList{Kind: kind, ResourceID: id}, s.err
}

func (s *shareRecorder) SetShare(_ context.Context, _ contract.Actor, kind, id, principal, role string) (ShareList, error) {
	s.calls = append(s.calls, "set "+kind+" "+id+" "+principal+" "+role)
	return ShareList{Kind: kind, ResourceID: id}, s.err
}

func (s *shareRecorder) RevokeShare(_ context.Context, _ contract.Actor, kind, id, principal string) (ShareList, error) {
	s.calls = append(s.calls, "revoke "+kind+" "+id+" "+principal)
	return ShareList{Kind: kind, ResourceID: id}, s.err
}

func (s *shareRecorder) SearchShareUsers(_ context.Context, _ contract.Actor, kind, id, q string, _ int) (UserList, error) {
	s.calls = append(s.calls, "users "+kind+" "+id+" "+q)
	return UserList{}, s.err
}

var _ = Describe("the generic sharing calls", func() {
	actor := contract.Actor{UserID: "alice", CredentialID: "key:a", Mode: "named-user"}
	allow := func(context.Context, contract.Actor) error { return nil }

	It("reach the owner's ShareManager with the kind and are classified as reads or mutations", func() {
		rec := &shareRecorder{}
		g := &Gateway{Sharing: rec, Revalidate: allow, RevalidateAccount: allow}
		ctx := context.Background()
		_, err := g.Shares(ctx, actor, "example:note")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.ShareGrants(ctx, actor, "example:note", "r1")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.SetShare(ctx, actor, "", "example:note", "r1", "@everyone", "use")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.RevokeShare(ctx, actor, "", "example:note", "r1", "bob")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.SearchShareUsers(ctx, actor, "example:note", "r1", "bo", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.calls).To(Equal([]string{"list example:note", "grants example:note r1", "set example:note r1 @everyone use", "revoke example:note r1 bob", "users example:note r1 bo"}))

		// A lost answer of a grant change may have changed the grants, so it
		// is an unknown outcome with its operation; a lost read is not.
		rec.err = ErrUnavailable
		_, err = g.SetShare(ctx, actor, "op-1", "example:note", "r1", "bob", "use")
		var unknown *OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("op-1"))
		_, err = g.RevokeShare(ctx, actor, "op-2", "example:note", "r1", "bob")
		Expect(errors.As(err, &unknown)).To(BeTrue())
		for _, read := range []error{
			func() error { _, e := g.Shares(ctx, actor, "example:note"); return e }(),
			func() error { _, e := g.ShareGrants(ctx, actor, "example:note", "r1"); return e }(),
			func() error { _, e := g.SearchShareUsers(ctx, actor, "example:note", "r1", "bo", 5); return e }(),
		} {
			Expect(errors.As(read, &unknown)).To(BeFalse())
			Expect(read).To(MatchError(ErrUnavailable))
		}
		for _, action := range []string{"resource-share-list", "resource-share-grants", "resource-share-users"} {
			Expect(mutation(action)).To(BeFalse(), action)
		}
		for _, action := range []string{"resource-share-set", "resource-share-revoke"} {
			Expect(mutation(action)).To(BeTrue(), action)
		}
	})

	It("are unavailable on an owner without a ShareManager", func() {
		g := &Gateway{Resources: nil, Revalidate: allow, RevalidateAccount: allow}
		_, err := g.Shares(context.Background(), actor, "example:note")
		Expect(err).To(MatchError(ErrUnavailable))
	})

	It("check the account alone, here and through the carrier, while the add-on calls keep the add-ons check", func() {
		addonsOff := func(context.Context, contract.Actor) error { return ErrForbidden }
		ctx := context.Background()

		// No account check at all denies the sharing calls.
		_, err := (&Gateway{Sharing: &shareRecorder{}, Revalidate: allow}).Shares(ctx, actor, "example:note")
		Expect(err).To(MatchError(ErrForbidden))

		rec := &shareRecorder{}
		local := &Gateway{Sharing: rec, Revalidate: addonsOff, RevalidateAccount: allow}
		_, err = local.Shares(ctx, frontActor, "example:note")
		Expect(err).NotTo(HaveOccurred())
		_, err = local.ResourceGrants(ctx, frontActor, "r1")
		Expect(err).To(MatchError(ErrForbidden))

		var refuse atomic.Bool
		account := func(context.Context, contract.Actor) error {
			if refuse.Load() {
				return ErrForbidden
			}
			return nil
		}
		owner := &Gateway{Sharing: rec, Revalidate: addonsOff, RevalidateAccount: account}
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		front := &Gateway{Forward: env.carriers["front-a"], Revalidate: allow, RevalidateAccount: allow}
		_, err = front.Shares(ctx, frontActor, "example:note")
		Expect(err).NotTo(HaveOccurred())
		_, err = front.SetShare(ctx, frontActor, "op-3", "example:note", "r1", "bob", "use")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.calls).To(Equal([]string{"list example:note", "list example:note", "set example:note r1 bob use"}))
		// The owner refuses an add-on resource call of the same caller.
		_, err = front.ResourceGrants(ctx, frontActor, "r1")
		Expect(err).To(MatchError(ErrForbidden))
		// And the owner still checks the account of a sharing call.
		refuse.Store(true)
		_, err = front.Shares(ctx, frontActor, "example:note")
		Expect(err).To(MatchError(ErrForbidden))
	})

	It("give a frontend the owner's typed refusal of a search that came too often", func() {
		rec := &shareRecorder{err: ErrSearchLimited}
		owner := &Gateway{Sharing: rec, Revalidate: allow, RevalidateAccount: allow}
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		front := &Gateway{Forward: env.carriers["front-a"], Revalidate: allow, RevalidateAccount: allow}
		_, err := front.SearchShareUsers(context.Background(), frontActor, "example:note", "r1", "bob", 5)
		Expect(err).To(MatchError(ErrSearchLimited))
		Expect(errors.Is(err, ErrForbidden)).To(BeTrue())
		Expect(errors.Is(err, ErrUnavailable)).To(BeFalse())
	})
})
