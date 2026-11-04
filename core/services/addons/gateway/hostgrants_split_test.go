// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("host access in a split deployment", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	actor := contract.Actor{UserID: "admin", CredentialID: "session:abc", Mode: "named-user"}

	setup := func(owner *hgFixture) (a, b *Gateway, ownerGW *Gateway) {
		var og *Gateway
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			og = &Gateway{HostGrantsOwner: owner.svc, Revalidate: ok}
			h, e := c.OwnerHandler(og)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		return &Gateway{Forward: cs["front-a"], Revalidate: ok}, &Gateway{Forward: cs["front-b"], Revalidate: ok}, og
	}
	grant := func(op string, rev uint64) HostGrantsCall {
		return call(HostGrantsEdit, op, "sandbox", HostGrantsEditRequest{OperationID: op, Revision: rev, Ops: []string{"egress.check"}})
	}

	It("forwards a write that carries the human mark and lets both frontends read the one revision", func() {
		owner := newHGFixture()
		a, b, _ := setup(owner)
		r := data[HostGrantsAddonResult](a.HostGrants(human, actor, grant("e1", 0)))
		Expect(r.Revision).To(Equal(uint64(1)))
		for _, gw := range []*Gateway{a, b} {
			v := data[HostGrantsView](gw.HostGrants(context.Background(), actor, call(HostGrantsRead, "", "", nil)))
			Expect(v.Revision).To(Equal(uint64(1)))
		}
		r = data[HostGrantsAddonResult](b.HostGrants(human, actor, grant("e2", 1)))
		Expect(r.Revision).To(Equal(uint64(2)))
		_, err := a.HostGrants(human, actor, grant("e3", 1))
		Expect(typeOf(err)).To(Equal("revision_conflict"), "a stale tab on the other frontend")
		snap, err := owner.store.Snapshot(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.Revision).To(Equal(uint64(2)))
	})

	It("refuses at the owner a write that the frontend did not mark, whatever else it holds", func() {
		owner := newHGFixture()
		a, _, _ := setup(owner)
		_, err := a.HostGrants(context.Background(), actor, grant("e1", 0))
		Expect(typeOf(err)).To(Equal("not_human_surface"))
		snap, _ := owner.store.Snapshot(context.Background())
		Expect(snap.Revision).To(BeZero())
	})

	It("lets the typed refusals of the owner through unchanged", func() {
		owner := newHGFixture(func(c *HostGrantsConfig) { c.Locked = true })
		a, _, _ := setup(owner)
		_, err := a.HostGrants(human, actor, grant("e1", 0))
		Expect(typeOf(err)).To(Equal("locked_by_deployment"))
		var he *HostGrantsError
		Expect(errors.As(err, &he)).To(BeTrue())
		Expect(he.Status).To(Equal(403))
	})

	It("never lets a frontend with its own service or flags decide: it forwards", func() {
		owner := newHGFixture(func(c *HostGrantsConfig) { c.Locked = true })
		a, _, _ := setup(owner)
		// A frontend whose own flags would allow everything.
		local := newHGFixture(func(c *HostGrantsConfig) {
			c.Floor = hostgrants.Floor{Grants: map[string][]string{"sandbox": {"workspace.", "egress."}}}
		})
		a.HostGrantsOwner = local.svc
		_, err := a.HostGrants(human, actor, grant("e1", 0))
		Expect(typeOf(err)).To(Equal("locked_by_deployment"), "the owner's decision")
		snap, _ := local.store.Snapshot(context.Background())
		Expect(snap.Revision).To(BeZero(), "nothing was stored on the frontend")
	})

	It("reports an owner without host access calls as unavailable with its reason, never as an unknown outcome", func() {
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Revalidate: ok})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		a := &Gateway{Forward: cs["front-a"], Revalidate: ok}
		_, err := a.HostGrants(human, actor, grant("e1", 0))
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(UnavailableReason(err)).To(Equal(ReasonHostGrantsUnsupported))
	})

	It("signs the human mark so it cannot be added after the fact", func() {
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		v := &ForwardVerifier{OwnerID: "owner", Keys: map[string]ed25519.PublicKey{"front": pub}, Revalidate: ok}
		peer := PeerIdentity{ID: "front", Verified: true}
		msg := func(nonce string, mark bool) ForwardedRequest {
			return ForwardedRequest{PeerID: "front", Audience: "owner", Actor: actor, OperationID: "op", Nonce: nonce, ExpiresAt: time.Now().Add(time.Minute), Body: []byte(`{"operationId":"op"}`), HumanSurface: mark}
		}
		m := msg("n1", true)
		m.Sign(priv)
		_, e = v.Verify(context.Background(), peer, m)
		Expect(e).NotTo(HaveOccurred())
		added := msg("n2", false)
		added.Sign(priv)
		added.HumanSurface = true
		_, e = v.Verify(context.Background(), peer, added)
		Expect(e).To(MatchError(ErrForbidden), "a mark cannot be added to a signed request")
		plain := msg("n3", false)
		plain.Sign(priv)
		Expect(string(plain.signingBytes())).NotTo(ContainSubstring("humanSurface"), "other requests sign as before")
	})

	It("does not carry the mark on a call that is not a host access call", func() {
		var seen []bool
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Revalidate: func(ctx context.Context, _ contract.Actor) error {
				seen = append(seen, HumanSurfaceOf(ctx))
				return nil
			}})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		_, _ = cs["front-a"].Dispatch(human, actor, contract.Request{OperationID: "op1", Action: "operation-status"})
		Expect(seen).NotTo(BeEmpty())
		Expect(seen).NotTo(ContainElement(true))
	})
})
