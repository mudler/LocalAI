package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("the owner's OAuth service", func() {
	ctx := context.Background()
	var (
		r     *oauthRig
		owner studio.OAuthOwner
	)
	session := func(user string) contract.Actor {
		return contract.Actor{UserID: user, CredentialID: "session:" + user, Mode: "named-user"}
	}
	BeforeEach(func() {
		r = newOAuthRig(connStore())
		owner = studio.OAuthOwner{
			Connectors: func() *studio.Connectors { return r.conns },
			Principal:  func(a contract.Actor) string { return a.UserID },
			Admin: func(_ context.Context, a contract.Actor) error {
				if a.UserID != "root" {
					return gateway.ErrForbidden
				}
				return nil
			},
		}
	})
	oauthErr := func(err error) *gateway.OAuthError {
		var oe *gateway.OAuthError
		ExpectWithOffset(1, errors.As(err, &oe)).To(BeTrue(), "%v", err)
		return oe
	}
	secret := func(s string) *string { return &s }

	It("starts and finishes the login of the person the actor names", func() {
		r.approve()
		b := r.binding()
		got, err := owner.ServeOAuth(ctx, session(alice), gateway.OAuthCall{Op: gateway.OAuthStart, BindingID: b.ID, Scopes: []string{"write"}, RedirectURI: oauthRedirect, ReturnTo: oauthReturn})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Nonce).NotTo(BeEmpty())
		u, err := url.Parse(got.AuthorizeURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(u.Query().Get("scope")).To(Equal("read write"))

		_, err = owner.ServeOAuth(ctx, session("mallory"), gateway.OAuthCall{Op: gateway.OAuthComplete, Nonce: got.Nonce, State: u.Query().Get("state"), Code: oauthCode})
		Expect(err).NotTo(HaveOccurred(), "a wrong person is an outcome, not an error")

		got, _ = owner.ServeOAuth(ctx, session(alice), gateway.OAuthCall{Op: gateway.OAuthStart, BindingID: b.ID, RedirectURI: oauthRedirect, ReturnTo: oauthReturn})
		u, _ = url.Parse(got.AuthorizeURL)
		done, err := owner.ServeOAuth(ctx, session(alice), gateway.OAuthCall{Op: gateway.OAuthComplete, Nonce: got.Nonce, State: u.Query().Get("state"), Code: oauthCode})
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(Equal(gateway.OAuthReply{Outcome: studio.LoginOK, ReturnTo: oauthReturn, BindingID: b.ID}))

		out, err := owner.ServeOAuth(ctx, session(alice), gateway.OAuthCall{Op: gateway.OAuthDisconnect, BindingID: b.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Revoked).To(BeTrue())
		_, err = r.st.GetGrant(ctx, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound))
	})

	It("types the refusals of the service with host text only", func() {
		b := r.binding()
		call := gateway.OAuthCall{Op: gateway.OAuthStart, BindingID: b.ID, RedirectURI: oauthRedirect, ReturnTo: oauthReturn}
		_, err := owner.ServeOAuth(ctx, session(alice), call)
		Expect(*oauthErr(err)).To(Equal(gateway.OAuthError{Status: 409, Type: gateway.OAuthNeedsApproval, Message: "an administrator must approve this add-on first"}))
		_, err = owner.ServeOAuth(ctx, session("mallory"), call)
		Expect(oauthErr(err).Status).To(Equal(404))
		r.approve()
		call.Scopes = []string{"admin"}
		_, err = owner.ServeOAuth(ctx, session(alice), call)
		Expect(*oauthErr(err)).To(Equal(gateway.OAuthError{Status: 400, Type: gateway.OAuthInvalid, Message: `the scope "admin" is not an optional scope of this add-on`}))

		call.Scopes = nil
		for {
			if _, err = owner.ServeOAuth(ctx, session(alice), call); err != nil {
				break
			}
		}
		Expect(*oauthErr(err)).To(Equal(gateway.OAuthError{Status: 429, Type: gateway.OAuthTooManyLogins,
			Message: "too many logins were started; finish one, or wait a few minutes and try again"}))

		broken := owner
		broken.Connectors = func() *studio.Connectors { return nil }
		_, err = broken.ServeOAuth(ctx, session(alice), call)
		Expect(oauthErr(err).Type).To(Equal(gateway.OAuthConnectorsOff))
		_, err = owner.ServeOAuth(ctx, contract.Actor{Mode: "named-user"}, call)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue(), "an actor that names no person")
	})

	It("lets only an administrator save a client, and saving is the approval", func() {
		set := gateway.OAuthCall{Op: gateway.OAuthClientSet, AddonID: oauthAddon, ClientID: "client-1", ClientSecret: secret(oauthSecret), Digest: r.digest()}
		_, err := owner.ServeOAuth(ctx, session(alice), set)
		Expect(oauthErr(err).Status).To(Equal(403))
		_, err = owner.ServeOAuth(ctx, session(alice), gateway.OAuthCall{Op: gateway.OAuthClientGet, AddonID: oauthAddon})
		Expect(oauthErr(err).Status).To(Equal(403), "a person reads no client")

		got, err := owner.ServeOAuth(ctx, session("root"), set)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Client.Approved).To(BeTrue())
		Expect(got.Client.HasSecret).To(BeTrue())
		Expect(got.Client.ApprovedBy).To(Equal("root"))
		raw, _ := json.Marshal(got)
		Expect(string(raw)).NotTo(ContainSubstring(oauthSecret))

		r.setDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Optional = append(d.Scopes.Optional, "admin") })
		_, err = owner.ServeOAuth(ctx, session("root"), set)
		Expect(*oauthErr(err)).To(Equal(gateway.OAuthError{Status: 409, Type: gateway.OAuthApprovalStale, Message: "the add-on's login changed; review it again", CurrentDigest: r.digest()}))
		read, err := owner.ServeOAuth(ctx, session("root"), gateway.OAuthCall{Op: gateway.OAuthClientGet, AddonID: oauthAddon})
		Expect(err).NotTo(HaveOccurred())
		Expect(read.Client.Approved).To(BeFalse())
		Expect(read.Client.CurrentDigest).To(Equal(r.digest()))
		raw, _ = json.Marshal(read)
		Expect(string(raw)).NotTo(ContainSubstring(oauthSecret))
	})

	It("is unavailable for an add-on whose descriptor it cannot read", func() {
		r.approve()
		b := r.binding()
		o := owner
		conns, err := studio.NewConnectors(r.svc, studio.ConnectorOptions{Store: r.st, Transport: r.tr})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		o.Connectors = func() *studio.Connectors { return conns }
		_, err = o.ServeOAuth(ctx, session(alice), gateway.OAuthCall{Op: gateway.OAuthStart, BindingID: b.ID, RedirectURI: oauthRedirect, ReturnTo: oauthReturn})
		Expect(*oauthErr(err)).To(Equal(gateway.OAuthError{Status: 503, Type: gateway.OAuthUnavailable, Message: "the login of this add-on is not available now; check that the add-on runs"}))
	})
})

var _ = Describe("the state of an OAuth connection in the lists", func() {
	ctx := context.Background()
	var r *oauthRig
	BeforeEach(func() { r = newOAuthRig(connStore()) })
	view := func(id string) *studio.OAuthView {
		i, err := r.conns.GetConnector(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		return i.OAuth
	}

	It("says what the person must do, from the shared store only", func() {
		b := r.binding()
		Expect(*view(b.ID)).To(Equal(studio.OAuthView{State: studio.OAuthNeedsApproval, Scopes: []string{}}))
		r.approve()
		Expect(*view(b.ID)).To(Equal(studio.OAuthView{State: studio.OAuthNotConnected, Scopes: []string{}, HasClient: true}))

		ls, state := r.start(b)
		_, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
		Expect(err).NotTo(HaveOccurred())
		v := view(b.ID)
		Expect(v.State).To(Equal(studio.OAuthConnected))
		Expect(v.AccountLabel).To(Equal(oauthAccountName))
		Expect(v.Scopes).To(Equal([]string{"read"}))
		Expect(v.ExpiresAt).NotTo(BeZero())
		Expect(v.HasClient).To(BeTrue())

		conns, err := r.conns.Connections(ctx, alice, oauthAddon)
		Expect(err).NotTo(HaveOccurred())
		Expect(conns).To(HaveLen(1))
		Expect(conns[0].State).To(Equal(studio.OAuthConnected))

		g := r.grant(b.ID)
		g.State, g.Reason = connectors.GrantReconnectNeeded, string(studio.ReasonRevoked)
		_, err = r.st.UpdateGrant(ctx, g, g.Version)
		Expect(err).NotTo(HaveOccurred())
		Expect(view(b.ID).State).To(Equal(studio.OAuthReconnectNeeded))
		Expect(view(b.ID).Reason).To(Equal(string(studio.ReasonRevoked)))
		conns, _ = r.conns.Connections(ctx, alice, oauthAddon)
		Expect(conns[0].State).To(Equal(studio.OAuthReconnectNeeded))

		g = r.grant(b.ID)
		g.State, g.Reason = connectors.GrantNeedsApproval, string(studio.ReasonNeedsApproval)
		_, err = r.st.UpdateGrant(ctx, g, g.Version)
		Expect(err).NotTo(HaveOccurred())
		Expect(view(b.ID).State).To(Equal(studio.OAuthNeedsApproval))
		Expect(view(b.ID).Reason).To(Equal(string(studio.ReasonNeedsApproval)))
		conns, _ = r.conns.Connections(ctx, alice, oauthAddon)
		Expect(conns[0].State).To(Equal(studio.OAuthNeedsApproval), "a held login is no reconnect")
	})

	It("has no view for a token binding", func() {
		// A token binding of an OAuth add-on cannot be created any more; one
		// stored earlier still reads.
		Expect(r.st.PutBinding(ctx, connectors.Binding{ID: "tok", Owner: alice, Kind: oauthKind, AddonID: oauthAddon, Name: "Token", Enabled: true, Token: []byte("sealed")})).To(Succeed())
		i, err := r.conns.GetConnector(ctx, alice, "tok")
		Expect(err).NotTo(HaveOccurred())
		Expect(i.OAuth).To(BeNil())
		conns, err := r.conns.Connections(ctx, alice, oauthAddon)
		Expect(err).NotTo(HaveOccurred())
		Expect(conns[0].State).To(BeEmpty())
	})
})
