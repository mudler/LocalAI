// SPDX-License-Identifier: MIT
package oauthtest_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOAuthTest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "oauthtest suite")
}

var _ = Describe("the fake OAuth service", func() {
	ctx := context.Background()
	const host = "login.provider-a.example.test"
	desc := func(clientAuth string) contract.OAuthDescriptor {
		return contract.OAuthDescriptor{AuthorizeURL: "https://" + host + "/authorize", TokenURL: "https://" + host + "/token", RevokeURL: "https://" + host + "/revoke",
			Hosts: []string{host}, Scopes: contract.OAuthScopes{Required: []string{"read"}}, PKCE: contract.OAuthPKCES256, ClientAuth: clientAuth}
	}
	// login gets a code for verifier through the authorize endpoint.
	login := func(n *oauthtest.Network, verifier string) string {
		sum := sha256.Sum256([]byte(verifier))
		q := url.Values{"response_type": {"code"}, "client_id": {"client-a"}, "redirect_uri": {"https://localai.example.test/cb"}, "scope": {"read"},
			"state": {"s"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
		back, err := n.Authorize(ctx, "https://"+host+"/authorize?"+q.Encode())
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Query().Get("state")).To(Equal("s"))
		return back.Query().Get("code")
	}

	It("serves the authorize endpoint at the path the config names", func() {
		p := oauthtest.New(oauthtest.Config{Host: host, ClientID: "client-a", ClientSecret: "secret", ClientAuth: contract.OAuthClientAuthPost, AuthorizePath: "/o/v2/auth"})
		DeferCleanup(p.Close)
		n := oauthtest.NewNetwork(p)
		q := url.Values{"response_type": {"code"}, "client_id": {"client-a"}, "redirect_uri": {"https://localai.example.test/cb"}, "scope": {"read"},
			"state": {"s"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"}}
		back, err := n.Authorize(ctx, "https://"+host+"/o/v2/auth?"+q.Encode())
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Query().Get("code")).NotTo(BeEmpty())
		_, err = n.Authorize(ctx, "https://"+host+"/authorize?"+q.Encode())
		Expect(err).To(HaveOccurred(), "the default path is not served when another is configured")
	})

	It("trades a code for the verifier it was issued for, with the client authentication it requires", func() {
		p := oauthtest.New(oauthtest.Config{Host: host, ClientID: "client-a", ClientSecret: "secret", ClientAuth: contract.OAuthClientAuthBasic})
		DeferCleanup(p.Close)
		n := oauthtest.NewNetwork(p)
		client := n.TokenClient()

		_, err := client.Exchange(ctx, desc(contract.OAuthClientAuthBasic), oauthtoken.Credentials{ID: "client-a", Secret: "secret"}, login(n, "v1"), "wrong-verifier", "https://localai.example.test/cb")
		Expect(err).To(MatchError(oauthtoken.ErrInvalidGrant))
		_, err = client.Exchange(ctx, desc(contract.OAuthClientAuthPost), oauthtoken.Credentials{ID: "client-a", Secret: "secret"}, login(n, "v2"), "v2", "https://localai.example.test/cb")
		Expect(err).To(MatchError(oauthtoken.ErrInvalidClient), "the secret in the form, where the service wants basic")
		tok, err := client.Exchange(ctx, desc(contract.OAuthClientAuthBasic), oauthtoken.Credentials{ID: "client-a", Secret: "secret"}, login(n, "v3"), "v3", "https://localai.example.test/cb")
		Expect(err).NotTo(HaveOccurred())
		Expect(tok.Refresh).NotTo(BeEmpty())
		auths := []string{}
		for _, c := range p.Calls() {
			auths = append(auths, c.Auth)
		}
		Expect(auths).To(Equal([]string{oauthtest.SeenBasic, oauthtest.SeenPost, oauthtest.SeenBasic}))
		Expect(p.Tokens.Load()).To(BeEquivalentTo(3))
	})

	It("sends the person back with access_denied when they decline", func() {
		p := oauthtest.New(oauthtest.Config{Host: host, ClientID: "client-a", ClientAuth: contract.OAuthClientAuthNone})
		DeferCleanup(p.Close)
		p.Set(func(p *oauthtest.Provider) { p.Deny = true })
		n := oauthtest.NewNetwork(p)
		back, err := n.Authorize(ctx, "https://"+host+"/authorize?"+url.Values{"response_type": {"code"}, "client_id": {"client-a"}, "redirect_uri": {"https://localai.example.test/cb"},
			"state": {"s"}, "code_challenge": {"c"}, "code_challenge_method": {"S256"}}.Encode())
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Query().Get("error")).To(Equal("access_denied"))
		Expect(back.Query().Get("code")).To(BeEmpty())
	})

	It("rotates the refresh token and refuses the used one", func() {
		p := oauthtest.New(oauthtest.Config{Host: host, ClientID: "client-a", ClientAuth: contract.OAuthClientAuthNone})
		DeferCleanup(p.Close)
		p.Set(func(p *oauthtest.Provider) { p.Rotate = true })
		n := oauthtest.NewNetwork(p)
		cred := oauthtoken.Credentials{ID: "client-a"}
		tok, err := n.TokenClient().Exchange(ctx, desc(contract.OAuthClientAuthNone), cred, login(n, "v"), "v", "https://localai.example.test/cb")
		Expect(err).NotTo(HaveOccurred())
		next, err := n.TokenClient().Refresh(ctx, desc(contract.OAuthClientAuthNone), cred, tok.Refresh)
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Refresh).NotTo(Equal(tok.Refresh))
		_, err = n.TokenClient().Refresh(ctx, desc(contract.OAuthClientAuthNone), cred, tok.Refresh)
		Expect(err).To(MatchError(oauthtoken.ErrInvalidGrant))
	})
})
