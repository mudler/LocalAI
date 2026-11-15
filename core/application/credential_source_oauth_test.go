// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtokentest"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// oauthTransport is tokenTransport for an add-on whose credential is an OAuth
// login.
type oauthTransport struct{ tokenTransport }

func (t oauthTransport) Kinds(ctx context.Context) ([]studio.ConnectorKind, error) {
	kinds, err := t.tokenTransport.Kinds(ctx)
	for i := range kinds {
		kinds[i].AuthKind = contract.CredentialKindOAuth2
	}
	return kinds, err
}

// The credential source gives an OAuth connection's access token to a call,
// renews it when the add-on rejects it, and reports an ended login with the
// reconnect_needed refusal.
var _ = Describe("the credential source with an OAuth connection", func() {
	ctx := context.Background()
	const user = "alice"
	actor := contract.Actor{UserID: user, CredentialID: "chats", Mode: "named-user"}
	var (
		src  credentialSource
		srv  *oauthtokentest.Server
		conn string
	)
	refusal := func(err error) string {
		var r *gateway.CredentialRefusal
		Expect(errors.As(err, &r)).To(BeTrue(), "error = %v", err)
		return r.Code
	}
	BeforeEach(func() {
		srv = oauthtokentest.New()
		DeferCleanup(srv.Close)
		srv.Set(func(s *oauthtokentest.Server) { s.ExpiresIn, s.Scope = 3600, "read" })
		chats, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: chats, Agents: agentStore, Provider: provider.New(), Harness: studiotest.NewGateHarness(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		bindings, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		desc := srv.Descriptor(contract.OAuthClientAuthPost)
		conns, err := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: oauthTransport{}, TokenClient: srv.Client(),
			OAuthDescriptor: func(context.Context, string) (contract.OAuthDescriptor, error) { return desc, nil }})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		svc.SetConnectors(conns)
		src = credentialSource{&Application{studioStore: chats, studioConnectors: conns}}

		secret := "client-secret"
		_, err = conns.SetClient(ctx, "tracker", studio.OAuthClientInput{Digest: desc.Digest(), ClientID: "client-1", Secret: &secret, ApprovedBy: "admin"})
		Expect(err).NotTo(HaveOccurred())
		b, err := conns.CreateConnector(ctx, user, studio.ConnectorInput{Kind: "tracker", Name: "Login", AuthKind: connectors.AuthOAuth2, Resources: []string{"acme/widgets"}})
		Expect(err).NotTo(HaveOccurred())
		conn = b.ID
		ls, err := conns.StartLogin(ctx, user, conn, nil, "https://localai.example.test/cb", "/app/tracker")
		Expect(err).NotTo(HaveOccurred())
		u, _ := url.Parse(ls.AuthorizeURL)
		res, err := conns.CompleteLogin(ctx, user, ls.Nonce, u.Query().Get("state"), "code", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Outcome).To(Equal(studio.LoginOK))
	})

	It("gives the access token, the granted scopes and the version of the login", func() {
		grant, err := src.Credential(ctx, actor, "tracker", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Credential).To(HavePrefix("access-"))
		Expect(grant.GrantedScopes).To(Equal([]string{"read"}))
		Expect(grant.Version).To(BeEquivalentTo(1))
		Expect(grant.BindingID).To(Equal(conn))
	})

	It("renews a rejected token once and ends the login on a second rejection", func() {
		grant, _ := src.Credential(ctx, actor, "tracker", "acme/widgets")
		renewed, err := src.RenewCredential(ctx, actor, "tracker", grant, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(renewed.Credential).NotTo(Equal(grant.Credential))
		Expect(renewed.Version).To(BeEquivalentTo(2))
		Expect(renewed.ResourceKey).To(Equal("acme/widgets"))
		_, err = src.RenewCredential(ctx, actor, "tracker", renewed, true)
		Expect(refusal(err)).To(Equal(gateway.CodeReconnectNeeded))
		_, err = src.Credential(ctx, actor, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeReconnectNeeded))
	})

	It("tells a person the connection is shared with that its owner must reconnect, never to log in", func() {
		_, err := src.answer("tracker", studio.CredentialGrant{}, fmt.Errorf("call: %w", studio.ErrOwnerReconnectNeeded))
		Expect(refusal(err)).To(Equal(gateway.CodeOwnerReconnectNeeded))
		Expect(gateway.CodeOwnerReconnectNeeded).NotTo(Equal(gateway.CodeReconnectNeeded))
	})

	It("reports a refused refresh as reconnect_needed, an outage as unavailable and a 429 as rate limited", func() {
		grant, _ := src.Credential(ctx, actor, "tracker", "acme/widgets")
		srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "server_error", 503 })
		_, err := src.RenewCredential(ctx, actor, "tracker", grant, false)
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus, s.RetryAfter = "slow_down", 429, "5" })
		_, err = src.RenewCredential(ctx, actor, "tracker", grant, false)
		var rl *contract.ErrRateLimited
		Expect(errors.As(err, &rl)).To(BeTrue(), "%v", err)
		srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "invalid_grant", 400 })
		_, err = src.RenewCredential(ctx, actor, "tracker", grant, false)
		Expect(refusal(err)).To(Equal(gateway.CodeReconnectNeeded))
	})
})
