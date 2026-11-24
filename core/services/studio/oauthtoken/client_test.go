package oauthtoken_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtokentest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("the token client", func() {
	ctx := context.Background()
	var (
		srv *oauthtokentest.Server
		c   *oauthtoken.Client
	)
	creds := oauthtoken.Credentials{ID: "client id", Secret: "client-secret-sentinel"}
	BeforeEach(func() {
		srv = oauthtokentest.New()
		DeferCleanup(srv.Close)
		c = srv.Client()
	})

	It("exchanges a code with the verifier and the redirect URI, and authenticates as the descriptor says", func() {
		for _, auth := range []string{contract.OAuthClientAuthBasic, contract.OAuthClientAuthPost, contract.OAuthClientAuthNone} {
			before := len(srv.Forms())
			srv.Set(func(s *oauthtokentest.Server) { s.ExpiresIn, s.Scope = 3600, "read write" })
			got, err := c.Exchange(ctx, srv.Descriptor(auth), creds, "the-code", "the-verifier", "https://localai.example.test/cb")
			Expect(err).NotTo(HaveOccurred(), auth)
			Expect(got.Access).To(HavePrefix("access-"))
			Expect(got.Refresh).To(HavePrefix("refresh-"))
			Expect(got.TokenType).To(Equal("Bearer"))
			Expect(got.ExpiresIn).To(Equal(time.Hour))
			Expect(got.Scopes).To(Equal([]string{"read", "write"}))
			Expect(got.ScopeSet).To(BeTrue())
			f := srv.Forms()[before]
			Expect(f.Get("grant_type")).To(Equal("authorization_code"))
			Expect(f.Get("code")).To(Equal("the-code"))
			Expect(f.Get("code_verifier")).To(Equal("the-verifier"))
			Expect(f.Get("redirect_uri")).To(Equal("https://localai.example.test/cb"))
			a := srv.Auths()[before]
			switch auth {
			case contract.OAuthClientAuthBasic:
				Expect(a).To(Equal(oauthtokentest.BasicAuth("client id", "client-secret-sentinel")))
				Expect(f.Has("client_secret")).To(BeFalse())
			case contract.OAuthClientAuthPost:
				Expect(a).To(BeEmpty())
				Expect(f.Get("client_id")).To(Equal("client id"))
				Expect(f.Get("client_secret")).To(Equal("client-secret-sentinel"))
			case contract.OAuthClientAuthNone:
				Expect(a).To(BeEmpty())
				Expect(f.Get("client_id")).To(Equal("client id"))
				Expect(f.Has("client_secret")).To(BeFalse())
			}
		}
	})

	It("refreshes with the refresh token and reports a rotated one", func() {
		srv.Set(func(s *oauthtokentest.Server) { s.Rotate = true })
		r := srv.Issue()
		got, err := c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Refresh).NotTo(BeEmpty())
		Expect(got.Refresh).NotTo(Equal(r))
		Expect(got.ExpiresIn).To(BeZero(), "a missing expires_in is unknown")
		Expect(got.ScopeSet).To(BeFalse())
		Expect(srv.Forms()[0].Get("refresh_token")).To(Equal(r))
		Expect(srv.Forms()[0].Get("grant_type")).To(Equal("refresh_token"))
		_, err = c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, r)
		Expect(err).To(MatchError(oauthtoken.ErrInvalidGrant), "a used refresh token of a rotating service")
	})

	It("clamps expires_in to one minute through one day", func() {
		for _, c2 := range []struct {
			in   int
			want time.Duration
		}{{1, time.Minute}, {-5, time.Minute}, {120, 2 * time.Minute}, {999999, 24 * time.Hour}} {
			srv.Set(func(s *oauthtokentest.Server) { s.ExpiresIn = c2.in })
			got, err := c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthNone), creds, srv.Issue())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.ExpiresIn).To(Equal(c2.want), "%d", c2.in)
		}
	})

	DescribeTable("classifies a failure into a fixed class",
		func(set func(s *oauthtokentest.Server), want error) {
			srv.Set(set)
			_, err := c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
			Expect(err).To(MatchError(want))
			Expect(oauthtokentest.HasNoSentinel(err.Error(), "client-secret-sentinel", "refresh-")).To(BeTrue(), err.Error())
		},
		Entry("invalid_grant", func(s *oauthtokentest.Server) { s.Fail = "invalid_grant" }, oauthtoken.ErrInvalidGrant),
		Entry("invalid_client", func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "invalid_client", 401 }, oauthtoken.ErrInvalidClient),
		Entry("unauthorized_client", func(s *oauthtokentest.Server) { s.Fail = "unauthorized_client" }, oauthtoken.ErrInvalidClient),
		Entry("another refusal", func(s *oauthtokentest.Server) { s.Fail = "invalid_scope" }, oauthtoken.ErrRefused),
		Entry("a 5xx", func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "server_error", 503 }, oauthtoken.ErrUnavailable),
		Entry("a token that is not a bearer token", func(s *oauthtokentest.Server) { s.TokenType = "mac" }, oauthtoken.ErrBadResponse),
		Entry("a token larger than a credential", func(s *oauthtokentest.Server) {
			s.Body = `{"access_token":"` + strings.Repeat("a", contract.MaxCredentialBytes+1) + `","token_type":"bearer"}`
		}, oauthtoken.ErrTokenTooLarge),
		Entry("a body over 64 KiB", func(s *oauthtokentest.Server) {
			s.Body = `{"access_token":"a","token_type":"bearer","x":"` + strings.Repeat("a", 64<<10) + `"}`
		}, oauthtoken.ErrBadResponse),
		Entry("no access token", func(s *oauthtokentest.Server) { s.Body = `{"token_type":"bearer"}` }, oauthtoken.ErrBadResponse),
		Entry("not JSON", func(s *oauthtokentest.Server) { s.Body = `refresh-sentinel` }, oauthtoken.ErrBadResponse),
		// A request refuses these granted scopes (contract.ValidScopeToken), so
		// a grant that held them would fail every call of the binding.
		Entry("a granted scope with a double quote", func(s *oauthtokentest.Server) { s.Scope = `read "x` }, oauthtoken.ErrBadResponse),
		Entry("a granted scope with a backslash", func(s *oauthtokentest.Server) { s.Scope = `read a\b` }, oauthtoken.ErrBadResponse),
		Entry("a granted scope that is too long", func(s *oauthtokentest.Server) {
			s.Scope = "read " + strings.Repeat("a", contract.MaxOAuthScopeBytes+1)
		}, oauthtoken.ErrBadResponse),
	)

	It("reports a token too large for a request as its own class of a bad response", func() {
		srv.Set(func(s *oauthtokentest.Server) {
			s.Body = `{"access_token":"` + strings.Repeat("a", contract.MaxCredentialBytes+1) + `","token_type":"bearer"}`
		})
		_, err := c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
		Expect(errors.Is(err, oauthtoken.ErrTokenTooLarge)).To(BeTrue())
		Expect(errors.Is(err, oauthtoken.ErrBadResponse)).To(BeTrue())
		srv.Set(func(s *oauthtokentest.Server) {
			s.Body = `{"access_token":"` + strings.Repeat("a", contract.MaxCredentialBytes) + `","token_type":"bearer"}`
		})
		_, err = c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
		Expect(err).NotTo(HaveOccurred(), "4096 bytes fit")
	})

	It("keeps only granted scopes that a request accepts", func() {
		srv.Set(func(s *oauthtokentest.Server) { s.Scope = "read write read https://scope.example.test/x" })
		got, err := c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Scopes).To(Equal([]string{"read", "write", "https://scope.example.test/x"}))
		Expect(contract.ValidGrantedScopes(got.Scopes)).To(BeTrue(), "the request check refuses what the client kept")
	})

	It("reports a 429 as rate limited with the wait the service asked for", func() {
		srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus, s.RetryAfter = "slow_down", 429, "17" })
		_, err := c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
		var rl *contract.ErrRateLimited
		Expect(errors.As(err, &rl)).To(BeTrue(), "%v", err)
		Expect(rl.RetryAfter).To(Equal(17))
		srv.Set(func(s *oauthtokentest.Server) { s.RetryAfter = "999999" })
		_, err = c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
		Expect(errors.As(err, &rl)).To(BeTrue())
		Expect(rl.RetryAfter).To(Equal(contract.MaxRetryAfter))
	})

	It("reports a timeout or a network failure as unavailable", func() {
		// The server holds its answer until the request ends.
		srv.Set(func(s *oauthtokentest.Server) { s.Gate = make(chan struct{}) })
		tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		_, err := c.Refresh(tctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, srv.Issue())
		Expect(err).To(MatchError(oauthtoken.ErrUnavailable))
		srv.Close()
		_, err = c.Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, "r")
		Expect(err).To(MatchError(oauthtoken.ErrUnavailable))
	})

	It("never sends anything for a descriptor that is not valid", func() {
		d := srv.Descriptor(contract.OAuthClientAuthBasic)
		d.TokenURL = "https://elsewhere.example.org/token"
		_, err := c.Refresh(ctx, d, creds, "r")
		Expect(err).To(MatchError(oauthtoken.ErrDescriptor))
		Expect(srv.Tokens.Load()).To(BeZero())
	})

	It("refuses a non-public address with the default client", func() {
		// The fake service answers on a loopback address that only the test
		// client dials; the default client reaches public addresses only.
		_, err := oauthtoken.New().Refresh(ctx, srv.Descriptor(contract.OAuthClientAuthBasic), creds, "r")
		Expect(err).To(MatchError(oauthtoken.ErrUnavailable))
		Expect(srv.Tokens.Load()).To(BeZero())
	})

	It("sends nothing for a descriptor that names localhost", func() {
		d := srv.Descriptor(contract.OAuthClientAuthBasic)
		d.Hosts, d.AuthorizeURL, d.TokenURL, d.RevokeURL = []string{"localhost"}, "https://localhost/a", "https://localhost/token", ""
		_, err := c.Refresh(ctx, d, creds, "r")
		Expect(err).To(MatchError(oauthtoken.ErrDescriptor))
		Expect(srv.Tokens.Load()).To(BeZero())
	})

	It("revokes a token at the revocation endpoint", func() {
		r := srv.Issue()
		Expect(c.Revoke(ctx, srv.Descriptor(contract.OAuthClientAuthPost), creds, r, "refresh_token")).To(Succeed())
		f := srv.Forms()[0]
		Expect(f.Get("token")).To(Equal(r))
		Expect(f.Get("token_type_hint")).To(Equal("refresh_token"))
		Expect(f.Get("client_secret")).To(Equal("client-secret-sentinel"))
		srv.Set(func(s *oauthtokentest.Server) { s.RevokeStatus = http.StatusServiceUnavailable })
		Expect(c.Revoke(ctx, srv.Descriptor(contract.OAuthClientAuthPost), creds, r, "refresh_token")).To(MatchError(oauthtoken.ErrUnavailable))
		srv.Set(func(s *oauthtokentest.Server) { s.RevokeStatus = http.StatusBadRequest })
		Expect(c.Revoke(ctx, srv.Descriptor(contract.OAuthClientAuthPost), creds, r, "refresh_token")).To(MatchError(oauthtoken.ErrRefused))
		d := srv.Descriptor(contract.OAuthClientAuthPost)
		d.RevokeURL = ""
		Expect(c.Revoke(ctx, d, creds, r, "")).To(MatchError(oauthtoken.ErrNoRevocation))
	})
})
