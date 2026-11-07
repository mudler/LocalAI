package studio_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// sharedInstance is a connector service on the rig's store and token endpoint
// that has a sharing policy, and the one the add-on calls go through.
func (r *oauthRig) sharedInstance(shares studio.ConnectionShares) *studio.Connectors {
	c, err := studio.NewConnectors(r.svc, studio.ConnectorOptions{
		Store: r.st, Transport: r.tr, Clock: r.sh.clock.Now, InstanceID: "front-shared", TokenClient: r.srv.Client(), Shares: shares,
		OAuthDescriptor: func(context.Context, string) (contract.OAuthDescriptor, error) {
			r.dmu.Lock()
			defer r.dmu.Unlock()
			return r.desc, nil
		},
		Logger: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(c.Close)
	r.tr.conns = c
	return c
}

// A connection with an OAuth login that its owner shared: the person it is
// shared with uses the owner's login through the sharing rules, and only
// that. They never hold a token, never start or end the login, and are told
// that the owner must connect again when the login ended.
var _ = Describe("an OAuth connection shared with another person", func() {
	ctx := context.Background()
	const bob = "bob"
	var (
		r      *oauthRig
		shares *fakeShares
		conns  *studio.Connectors
		b      connectors.Binding
	)
	BeforeEach(func() {
		r = newOAuthRig(connStore())
		shares = newFakeShares()
		conns = r.sharedInstance(shares)
		r.conns = conns
		b = r.connect()
		shares.share(b.ID, bob)
	})

	It("lets the grantee call with the access token of the owner's login, inside the share, and nothing else of it", func() {
		list, err := conns.Connections(ctx, bob, oauthAddon)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0]).To(MatchFields(IgnoreExtras, Fields{"ID": Equal(b.ID), "Shared": BeTrue(), "State": Equal(studio.OAuthConnected)}))

		own, err := conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		grant, err := conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal(own.Secret), "the same login")
		Expect(grant.Scopes).To(Equal([]string{"read"}))
		Expect(grant.Hold).NotTo(BeNil(), "the call is admitted work of the share")
		Expect(own.Hold).To(BeNil())
		ran := false
		Expect(grant.Hold(ctx, func(context.Context) error { ran = true; return nil })).To(Succeed())
		Expect(ran).To(BeTrue())

		// The grant that leaves the service holds the access token and the
		// scopes; no refresh token, client secret, code or verifier is in it,
		// and none is in what the grantee can list.
		text := fmt.Sprintf("%+v %+v", grant, list)
		Expect(text).NotTo(ContainSubstring("refresh-"))
		Expect(text).NotTo(ContainSubstring(oauthSecret))
		Expect(text).NotTo(ContainSubstring(oauthCode))
		Expect(strings.Count(r.logs.String(), grant.Secret)).To(BeZero(), "the token is not logged")

		_, err = conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/other")
		var refused *connectors.NotAllowedError
		Expect(errors.As(err, &refused)).To(BeTrue())
		Expect(refused.Allowed).To(BeEmpty(), "the grantee never learns the owner's list")
	})

	It("renews the login of the owner for the grantee, and the owner keeps one login", func() {
		first, err := conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		r.sh.clock.Advance(2 * time.Hour)
		next, err := conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Secret).NotTo(Equal(first.Secret))
		Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2))
		ownerSees, err := conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(ownerSees.Secret).To(Equal(next.Secret))

		// The add-on rejected the token: the grantee's call renews it too.
		renewed, err := conns.RenewCredential(ctx, bob, oauthAddon, b.ID, "acme/widgets", next.Version, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(renewed.Secret).NotTo(Equal(next.Secret))
		Expect(renewed.Hold).NotTo(BeNil())
	})

	It("keeps the login, its start and its end to the owner", func() {
		_, err := conns.StartLogin(ctx, bob, b.ID, nil, oauthRedirect, oauthReturn)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = conns.Disconnect(ctx, bob, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		Expect(conns.DeleteConnector(ctx, bob, b.ID)).To(MatchError(connectors.ErrNotFound))
		_, err = conns.GetConnector(ctx, bob, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound), "the grantee does not see the binding, its login or its account")
		Expect(r.grant(b.ID).State).To(Equal(connectors.GrantConnected))
		_, err = conns.GetConnector(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred(), "the binding is still there")
	})

	It("gives a person who is not a grantee nothing, also for a renewal", func() {
		_, err := conns.CredentialFor(ctx, "mallory", oauthAddon, b.ID, "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		_, err = conns.RenewCredential(ctx, "mallory", oauthAddon, b.ID, "acme/widgets", 1, false)
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		shares.revoke(b.ID, bob)
		_, err = conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential), "a revoked share ends the use at once")
		_, err = conns.RenewCredential(ctx, bob, oauthAddon, b.ID, "acme/widgets", 1, false)
		Expect(err).To(MatchError(connectors.ErrNoCredential))
	})

	Describe("when the login of the owner ended", func() {
		It("tells the grantee that the owner must reconnect, and the owner to reconnect", func() {
			first, err := conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
			Expect(err).NotTo(HaveOccurred())
			// The service rejects the token twice: the second end is final.
			renewed, err := conns.RenewCredential(ctx, bob, oauthAddon, b.ID, "acme/widgets", first.Version, false)
			Expect(err).NotTo(HaveOccurred())
			_, err = conns.RenewCredential(ctx, bob, oauthAddon, b.ID, "acme/widgets", renewed.Version, true)
			Expect(err).To(MatchError(studio.ErrOwnerReconnectNeeded))
			Expect(errors.Is(err, studio.ErrReconnectNeeded)).To(BeFalse(), "no login prompt for the grantee")

			_, err = conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/widgets")
			Expect(err).To(MatchError(studio.ErrOwnerReconnectNeeded))
			_, err = conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
			Expect(err).To(MatchError(studio.ErrReconnectNeeded), "the owner is told to connect again")

			list, err := conns.Connections(ctx, bob, oauthAddon)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1), "the connection stays listed, so a call gets the message")
			Expect(list[0].State).To(Equal(studio.OAuthReconnectNeeded))
			Expect(list[0].Shared).To(BeTrue())

			views, err := conns.SharedConnectors(ctx, bob)
			Expect(err).NotTo(HaveOccurred())
			Expect(views).To(HaveLen(1))
			Expect(views[0].Active).To(BeFalse())
			Expect(views[0].Reason).To(ContainSubstring("owner"))
			Expect(views[0].Reason).NotTo(ContainSubstring(string(studio.ReasonCredentialRejected)), "no reason code of the login")
		})

		It("tells the grantee the same when the owner disconnected", func() {
			_, err := conns.Disconnect(ctx, alice, b.ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = conns.CredentialFor(ctx, bob, oauthAddon, b.ID, "acme/widgets")
			Expect(err).To(MatchError(studio.ErrOwnerReconnectNeeded))
			list, err := conns.Connections(ctx, bob, oauthAddon)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].State).To(Equal(studio.OAuthReconnectNeeded))
		})
	})
})
