// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	studioendpoint "github.com/mudler/LocalAI/core/http/endpoints/studio"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtokentest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/clientip"
)

const (
	oauthAddonID  = "providerA"
	oauthKindID   = "providera"
	oauthAccount  = "someone@example.test"
	oauthSecretV  = "client-secret-sentinel"
	oauthBase     = "https://localai.example.test"
	oauthCallback = oauthBase + "/api/chats/connectors/oauth/callback"
)

// oauthAddonTransport is a connector add-on that serves one kind and answers
// "me" with the account.
type oauthAddonTransport struct{}

func (oauthAddonTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	return []studio.ConnectorKind{{AddonID: oauthAddonID, AuthKind: contract.CredentialKindOAuth2, ConnectorKind: contract.ConnectorKind{ID: oauthKindID, Title: "Provider A", AgentOptional: true}}}, nil
}

func (oauthAddonTransport) Call(context.Context, studio.ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{BotUsername: oauthAccount}, nil
}

// syncBuffer is a log sink several goroutines write.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// oauthEnv is one cluster for the OAuth specs: a token endpoint, a shared
// account database and connector store, and the servers built on them.
type oauthEnv struct {
	srv   *oauthtokentest.Server
	clock *fakeClock
	logs  *syncBuffer
	desc  contract.OAuthDescriptor
	dmu   sync.Mutex
	base  string // the external base URL the servers are given; "" for none
}

func (env *oauthEnv) descriptor(context.Context, string) (contract.OAuthDescriptor, error) {
	env.dmu.Lock()
	defer env.dmu.Unlock()
	return env.desc, nil
}

func (env *oauthEnv) changeDescriptor(f func(*contract.OAuthDescriptor)) {
	env.dmu.Lock()
	defer env.dmu.Unlock()
	f(&env.desc)
}

// connectorOptions gives a server the add-on transport, the clock and the
// log sink, and, on the owner only, the descriptors and the token client.
func (env *oauthEnv) connectorOptions(owner bool) func(*studio.ConnectorOptions) {
	return func(o *studio.ConnectorOptions) {
		o.Transport = oauthAddonTransport{}
		o.Clock = env.clock.Now
		o.Logger = slog.New(slog.NewTextHandler(env.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		if owner {
			o.OAuthDescriptor = env.descriptor
			o.TokenClient = env.srv.Client()
		}
	}
}

// ownerGateway is the gateway of the process that runs the add-ons.
func ownerGateway(s *studioServer) *gateway.Gateway {
	return &gateway.Gateway{
		Revalidate: auth.AddonRevalidate(s.db, auth.AddonModeNamedUser),
		OAuthOwner: studio.OAuthOwner{
			Connectors: func() *studio.Connectors { return s.conns },
			Principal:  func(a contract.Actor) string { return a.UserID },
			Admin: func(ctx context.Context, a contract.Actor) error {
				return auth.ValidateAddonActor(ctx, s.db, a, true)
			},
		},
	}
}

func dispatchTo(g *gateway.Gateway) studioendpoint.OAuthDispatcher { return g }

// build makes one server of the cluster. gw returns the gateway the server's
// OAuth routes use.
func (env *oauthEnv) build(owner bool, gw func(*studioServer) *gateway.Gateway) *studioServer {
	studioConnectorOptions = env.connectorOptions(owner)
	studioRouteOptions = func(s *studioServer) []studioendpoint.HandlerOption {
		return []studioendpoint.HandlerOption{studioendpoint.WithOAuth(studioendpoint.OAuthConfig{Mode: auth.AddonModeNamedUser,
			Dispatcher: func() studioendpoint.OAuthDispatcher { return dispatchTo(gw(s)) }})}
	}
	DeferCleanup(func() { studioConnectorOptions, studioRouteOptions = nil, nil })
	s := newStudioServer()
	s.e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if env.base != "" {
				c.Set("_external_base_url", env.base)
			}
			return next(c)
		}
	})
	for _, id := range []string{"alice", "bob"} {
		Expect(auth.UpdateUserPermissions(s.db, id, auth.PermissionMap{auth.FeatureAgents: true, auth.FeatureTriggers: true, auth.FeatureConnectors: true,
			auth.FeatureMCPJobs: true, auth.FeaturePages: true, auth.FeatureAddons: true})).To(Succeed())
	}
	return s
}

func newOAuthEnv() *oauthEnv {
	env := &oauthEnv{srv: oauthtokentest.New(), clock: &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, logs: &syncBuffer{}, base: oauthBase}
	DeferCleanup(env.srv.Close)
	env.srv.Set(func(s *oauthtokentest.Server) { s.ExpiresIn = 3600 })
	env.desc = env.srv.Descriptor(contract.OAuthClientAuthBasic)
	// The gateway and the routes log through xlog.
	xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(env.logs, &slog.HandlerOptions{Level: slog.LevelDebug}), xlog.LogLevelDebug))
	DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })
	return env
}

// oauthDo sends a request with the session of user (none for "") and the
// cookies given.
func oauthDo(s *studioServer, user, method, path string, body any, mod func(*http.Request), cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := s.request(user, method, path, body)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

// nonceCookie is the nonce cookie a response sets or clears: one per login,
// named after the hash of its state.
func nonceCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "localai_oauth_nonce_") {
			return c
		}
	}
	return nil
}

// login is a started login: the state from the authorize URL and the nonce
// cookie the start set.
type login struct {
	authorize *url.URL
	state     string
	cookie    *http.Cookie
}

func startLogin(s *studioServer, user, binding string, body any) login {
	rec := oauthDo(s, user, http.MethodPost, "/api/chats/connectors/"+binding+"/oauth/start", body, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	u, err := url.Parse(decodeJSON(rec)["authorize_url"].(string))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ck := nonceCookie(rec)
	ExpectWithOffset(1, ck).NotTo(BeNil())
	return login{authorize: u, state: u.Query().Get("state"), cookie: &http.Cookie{Name: ck.Name, Value: ck.Value}}
}

func callback(s *studioServer, user string, query url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return oauthDo(s, user, http.MethodGet, "/api/chats/connectors/oauth/callback?"+query.Encode(), nil, nil, cookies...)
}

func approveClient(s *studioServer) map[string]any {
	rec := oauthDo(s, "root", http.MethodGet, "/api/chats/admin/oauth-clients/"+oauthAddonID, nil, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	digest := decodeJSON(rec)["current_digest"].(string)
	rec = oauthDo(s, "root", http.MethodPut, "/api/chats/admin/oauth-clients/"+oauthAddonID,
		map[string]any{"client_id": "client-1", "client_secret": oauthSecretV, "descriptor_digest": digest}, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	return decodeJSON(rec)
}

func createOAuthBinding(s *studioServer, user string) string {
	rec := oauthDo(s, user, http.MethodPost, "/api/chats/connectors", map[string]any{"kind": oauthKindID, "name": "Work", "auth_kind": "oauth2", "resources": []string{"acme/widgets"}}, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
	out := decodeJSON(rec)
	ExpectWithOffset(1, out["auth_kind"]).To(Equal("oauth2"))
	return out["id"].(string)
}

func location(rec *httptest.ResponseRecorder) *url.URL {
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusSeeOther), rec.Body.String())
	u, err := url.Parse(rec.Header().Get("Location"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return u
}

func binding(s *studioServer, user, id string) map[string]any {
	rec := oauthDo(s, user, http.MethodGet, "/api/chats/connectors/"+id, nil, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	return decodeJSON(rec)
}

var _ = Describe("OAuth routes of the connections", func() {
	var (
		env *oauthEnv
		s   *studioServer
	)
	BeforeEach(func() {
		env = newOAuthEnv()
		s = env.build(true, ownerGateway)
	})

	It("needs a session on every route", func() {
		for _, r := range [][2]string{
			{http.MethodPost, "/api/chats/connectors/b1/oauth/start"},
			{http.MethodPost, "/api/chats/connectors/b1/oauth/disconnect"},
			{http.MethodGet, "/api/chats/connectors/oauth/callback?state=s&code=c"},
			{http.MethodGet, "/api/chats/admin/oauth-clients/" + oauthAddonID},
			{http.MethodPut, "/api/chats/admin/oauth-clients/" + oauthAddonID},
		} {
			Expect(oauthDo(s, "", r[0], r[1], nil, nil).Code).To(Equal(http.StatusUnauthorized), r[1])
		}
		Expect(oauthDo(s, "dave", http.MethodPost, "/api/chats/connectors/b1/oauth/start", nil, nil).Code).To(Equal(http.StatusForbidden), "the connectors feature")
	})

	It("routes the callback to its handler, not to a binding route", func() {
		for _, r := range [][3]string{
			{http.MethodGet, "/api/chats/connectors/oauth/callback", "/api/chats/connectors/oauth/callback"},
			{http.MethodPost, "/api/chats/connectors/oauth/oauth/start", "/api/chats/connectors/:id/oauth/start"},
			{http.MethodGet, "/api/chats/connectors/oauth", "/api/chats/connectors/:id"},
		} {
			c := s.e.NewContext(httptest.NewRequest(r[0], r[1], nil), httptest.NewRecorder())
			s.e.Router().Find(r[0], r[1], c)
			Expect(c.Path()).To(Equal(r[2]), r[0]+" "+r[1])
		}
		// Through the whole chain the callback always answers with a redirect.
		Expect(location(callback(s, "alice", url.Values{})).Query().Get("class")).To(Equal("invalid_state"))
	})

	It("logs a person in, connects the binding and works once", func() {
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		Expect(binding(s, "alice", id)["oauth"]).To(Equal(map[string]any{"state": "not_connected", "scopes": []any{}, "has_client": true}))

		rec := oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/start", map[string]any{"scopes": []string{"write"}, "return_to": "/app/chats/connectors?tab=mine"}, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		out := decodeJSON(rec)
		Expect(out).To(HaveKey("expires"))
		Expect(out).NotTo(HaveKey("nonce"))
		u, _ := url.Parse(out["authorize_url"].(string))
		Expect(u.Query().Get("redirect_uri")).To(Equal(oauthCallback))
		Expect(u.Query().Get("scope")).To(Equal("read write"))
		ck := nonceCookie(rec)
		Expect(ck).NotTo(BeNil())
		Expect(ck.HttpOnly).To(BeTrue())
		Expect(ck.SameSite).To(Equal(http.SameSiteLaxMode))
		Expect(ck.MaxAge).To(BeNumerically("~", 600, 2))
		Expect(ck.Secure).To(BeFalse(), "the request was plain HTTP")
		Expect(rec.Body.String()).NotTo(ContainSubstring(ck.Value), "the nonce is in the cookie only")

		done := callback(s, "alice", url.Values{"state": {u.Query().Get("state")}, "code": {"code-1"}}, &http.Cookie{Name: ck.Name, Value: ck.Value})
		loc := location(done)
		Expect(loc.Path).To(Equal("/app/chats/connectors"))
		Expect(loc.Query()).To(Equal(url.Values{"oauth": {"ok"}, "tab": {"mine"}}))
		Expect(done.Header().Get("Cache-Control")).To(Equal("no-store"))
		Expect(done.Header().Get("Referrer-Policy")).To(Equal("no-referrer"))
		cleared := nonceCookie(done)
		Expect(cleared).NotTo(BeNil())
		Expect(cleared.MaxAge).To(BeNumerically("<", 0))

		o := binding(s, "alice", id)["oauth"].(map[string]any)
		Expect(o["state"]).To(Equal("connected"))
		Expect(o["account_label"]).To(Equal(oauthAccount))
		Expect(o["scopes"]).To(Equal([]any{"read", "write"}))
		Expect(o).To(HaveKey("expires_at"))
		list := decodeJSON(oauthDo(s, "alice", http.MethodGet, "/api/chats/connectors", nil, nil))["connectors"].([]any)
		Expect(list[0].(map[string]any)["oauth"].(map[string]any)["state"]).To(Equal("connected"))

		again := callback(s, "alice", url.Values{"state": {u.Query().Get("state")}, "code": {"code-1"}}, &http.Cookie{Name: ck.Name, Value: ck.Value})
		Expect(location(again).Query()).To(Equal(url.Values{"oauth": {"error"}, "class": {"invalid_state"}}), "a replayed state")
		Expect(location(again).Path).To(Equal("/app/chats/connectors"))
	})

	It("finishes two logins started in one browser, each with its own nonce cookie", func() {
		approveClient(s)
		first, second := createOAuthBinding(s, "alice"), ""
		rec := oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors", map[string]any{"kind": oauthKindID, "name": "Home", "auth_kind": "oauth2"}, nil)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		second = decodeJSON(rec)["id"].(string)
		a, b := startLogin(s, "alice", first, nil), startLogin(s, "alice", second, nil)
		Expect(a.cookie.Name).NotTo(Equal(b.cookie.Name))
		Expect(a.cookie.Name).To(MatchRegexp(`^localai_oauth_nonce_[0-9a-f]{8}$`))
		Expect(a.cookie.Name).NotTo(ContainSubstring(a.state), "the name tells nothing of the state")

		// The browser sends both cookies to both callbacks.
		done := callback(s, "alice", url.Values{"state": {a.state}, "code": {"c"}}, a.cookie, b.cookie)
		Expect(location(done).Query().Get("oauth")).To(Equal("ok"))
		cleared := nonceCookie(done)
		Expect(cleared.Name).To(Equal(a.cookie.Name), "only the cookie of this login is cleared")
		Expect(cleared.MaxAge).To(BeNumerically("<", 0))
		done = callback(s, "alice", url.Values{"state": {b.state}, "code": {"c"}}, b.cookie)
		Expect(location(done).Query().Get("oauth")).To(Equal("ok"))
		for _, id := range []string{first, second} {
			Expect(binding(s, "alice", id)["oauth"].(map[string]any)["state"]).To(Equal("connected"))
		}
		// A callback without a state clears no cookie of a login in progress.
		c := startLogin(s, "alice", first, nil)
		Expect(nonceCookie(callback(s, "alice", url.Values{}, c.cookie))).To(BeNil())
	})

	It("refuses the callback of another person, another browser and an expired state", func() {
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		l := startLogin(s, "alice", id, nil)
		Expect(location(callback(s, "bob", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie)).Query().Get("class")).To(Equal("invalid_state"))

		l = startLogin(s, "alice", id, nil)
		Expect(location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, &http.Cookie{Name: l.cookie.Name, Value: "another-browser"})).Query().Get("class")).To(Equal("invalid_state"))
		l = startLogin(s, "alice", id, nil)
		Expect(location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}})).Query().Get("class")).To(Equal("invalid_state"), "no cookie")

		l = startLogin(s, "alice", id, nil)
		env.clock.Advance(connectors.LoginStateTTL + time.Second)
		Expect(location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie)).Query().Get("class")).To(Equal("invalid_state"))
		Expect(env.srv.Tokens.Load()).To(BeZero(), "no code was traded")
		Expect(binding(s, "alice", id)["oauth"].(map[string]any)["state"]).To(Equal("not_connected"))
	})

	It("reports a denial without touching the connection, and each error as its class", func() {
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		l := startLogin(s, "alice", id, nil)
		Expect(location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie)).Query().Get("oauth")).To(Equal("ok"))

		l = startLogin(s, "alice", id, map[string]any{"return_to": "/app/addons/providerA/settings"})
		loc := location(callback(s, "alice", url.Values{"state": {l.state}, "error": {"access_denied"}, "error_description": {"description-sentinel"}}, l.cookie))
		Expect(loc.String()).To(Equal("/app/addons/providerA/settings?oauth=denied"))
		o := binding(s, "alice", id)["oauth"].(map[string]any)
		Expect(o["state"]).To(Equal("connected"), "the login stays as it was")
		Expect(o["account_label"]).To(Equal(oauthAccount))

		class := func(q url.Values) string {
			l := startLogin(s, "alice", id, nil)
			q.Set("state", l.state)
			loc := location(callback(s, "alice", q, l.cookie))
			Expect(loc.Query().Get("oauth")).To(Equal("error"))
			Expect(loc.String()).NotTo(ContainSubstring("sentinel"))
			return loc.Query().Get("class")
		}
		Expect(class(url.Values{"error": {"server_error"}, "error_description": {"description-sentinel"}})).To(Equal("provider_error"))
		Expect(class(url.Values{})).To(Equal("provider_error"), "no code")
		env.srv.Set(func(s *oauthtokentest.Server) { s.Fail = "invalid_grant" })
		Expect(class(url.Values{"code": {"c"}})).To(Equal("exchange_failed"))
		env.srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "server_error", 503 })
		Expect(class(url.Values{"code": {"c"}})).To(Equal("unavailable"))
		env.srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus, s.Scope = "", 0, "write" })
		Expect(class(url.Values{"code": {"c"}})).To(Equal("scopes_missing"))
		env.srv.Set(func(s *oauthtokentest.Server) {
			s.Scope, s.Body = "", `{"access_token":"`+strings.Repeat("a", 4097)+`","token_type":"bearer"}`
		})
		Expect(class(url.Values{"code": {"c"}})).To(Equal("token_too_large"))
		Expect(env.logs.String()).NotTo(ContainSubstring("description-sentinel"))
	})

	DescribeTable("refuses a return page that is not a page of the web app",
		func(returnTo string) {
			approveClient(s)
			id := createOAuthBinding(s, "alice")
			rec := oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/start", map[string]any{"return_to": returnTo}, nil)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
			Expect(nonceCookie(rec)).To(BeNil())
		},
		Entry("another host", "https://elsewhere.example.test/app"),
		Entry("a protocol-relative address", "//elsewhere.example.test/app"),
		Entry("an API route", "/api/chats"),
		Entry("a lookalike prefix", "/appx/settings"),
		Entry("a dot segment", "/app/../api/chats"),
		Entry("an encoded dot segment", "/app/%2e%2e/api"),
		Entry("an encoded slash", "/app/a%2fb"),
		Entry("a fragment", "/app/chats#x"),
		Entry("a query with markup", "/app/chats?x=<b>"),
		Entry("a backslash", `/app\chats`),
	)

	Describe("the redirect address", func() {
		var id string
		BeforeEach(func() {
			approveClient(s)
			id = createOAuthBinding(s, "alice")
		})
		send := func(host string, mod func(*http.Request)) *httptest.ResponseRecorder {
			return oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/start", nil, func(r *http.Request) {
				r.Host = host
				if mod != nil {
					mod(r)
				}
			})
		}
		redirect := func(host string, mod func(*http.Request)) string {
			rec := send(host, mod)
			ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			u, _ := url.Parse(decodeJSON(rec)["authorize_url"].(string))
			return u.Query().Get("redirect_uri")
		}
		refused := func(host string, mod func(*http.Request)) {
			rec := send(host, mod)
			ExpectWithOffset(1, rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
			ExpectWithOffset(1, decodeJSON(rec)["error"].(map[string]any)["type"]).To(Equal("base_url_required"))
			ExpectWithOffset(1, nonceCookie(rec)).To(BeNil())
		}

		It("uses LOCALAI_BASE_URL when it is set", func() {
			Expect(redirect("anything.example.test", nil)).To(Equal(oauthCallback))
		})

		It("refuses a host it cannot trust, also with forwarding headers from an untrusted peer", func() {
			env.base = ""
			refused("localai.example.test", nil)
			refused("localai.example.test", func(r *http.Request) {
				r.Header.Set("X-Forwarded-Host", "localhost")
				r.Header.Set("X-Forwarded-Proto", "https")
			})
		})

		It("accepts a loopback host", func() {
			env.base = ""
			for host, want := range map[string]string{
				"localhost:8080": "http://localhost:8080/api/chats/connectors/oauth/callback",
				"127.0.0.1:8080": "http://127.0.0.1:8080/api/chats/connectors/oauth/callback",
				"[::1]:8080":     "http://[::1]:8080/api/chats/connectors/oauth/callback",
			} {
				Expect(redirect(host, nil)).To(Equal(want), host)
			}
		})

		It("refuses a plain http address unless the host is loopback or the administrator set it", func() {
			env.base = ""
			resolver, err := clientip.New([]string{"192.0.2.0/24"})
			Expect(err).NotTo(HaveOccurred())
			s.e.Pre(middleware.TrustedProxies(resolver))
			refused("10.0.0.5:8080", func(r *http.Request) {
				r.RemoteAddr = "192.0.2.10:4444"
				r.Header.Set("X-Forwarded-Host", "chat.example.test")
				r.Header.Set("X-Forwarded-Proto", "http")
			})
			env.base = "http://chat.example.test"
			Expect(redirect("anything.example.test", nil)).To(Equal("http://chat.example.test/api/chats/connectors/oauth/callback"),
				"an administrator chose this address")
		})

		It("believes the forwarding headers of a trusted proxy, and sets a secure cookie for https", func() {
			env.base = ""
			resolver, err := clientip.New([]string{"192.0.2.0/24"})
			Expect(err).NotTo(HaveOccurred())
			s.e.Pre(middleware.TrustedProxies(resolver))
			Expect(redirect("10.0.0.5:8080", func(r *http.Request) {
				r.Header.Set("X-Forwarded-Host", "chat.example.test")
				r.Header.Set("X-Forwarded-Proto", "https")
			})).To(Equal("https://chat.example.test/api/chats/connectors/oauth/callback"))
			rec := oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/start", nil, func(r *http.Request) {
				r.Host = "10.0.0.5:8080"
				r.Header.Set("X-Forwarded-Proto", "https")
			})
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(nonceCookie(rec).Secure).To(BeTrue())
		})
	})

	It("lets only an administrator read and save the client, refuses a stale digest and never returns the secret", func() {
		Expect(oauthDo(s, "alice", http.MethodGet, "/api/chats/admin/oauth-clients/"+oauthAddonID, nil, nil).Code).To(Equal(http.StatusForbidden))
		Expect(oauthDo(s, "alice", http.MethodPut, "/api/chats/admin/oauth-clients/"+oauthAddonID, map[string]any{"client_id": "x", "client_secret": oauthSecretV, "descriptor_digest": "d"}, nil).Code).To(Equal(http.StatusForbidden))

		rec := oauthDo(s, "root", http.MethodGet, "/api/chats/admin/oauth-clients/"+oauthAddonID, nil, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		got := decodeJSON(rec)
		Expect(got["configured"]).To(BeFalse())
		Expect(got["has_secret"]).To(BeFalse())
		Expect(got["callback_url"]).To(Equal(oauthCallback))
		desc := got["descriptor"].(map[string]any)
		Expect(desc["token_url"]).To(Equal("https://" + oauthtokentest.Host + "/token"))
		Expect(desc["required_scopes"]).To(Equal([]any{"read"}))
		Expect(desc["optional_scopes"]).To(Equal([]any{"write"}))
		Expect(desc["client_auth"]).To(Equal("basic"))
		digest := got["current_digest"].(string)

		rec = oauthDo(s, "root", http.MethodPut, "/api/chats/admin/oauth-clients/"+oauthAddonID, map[string]any{"client_id": "client-1", "client_secret": oauthSecretV, "descriptor_digest": "not-" + digest}, nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		conflict := decodeJSON(rec)
		Expect(conflict["current_digest"]).To(Equal(digest))
		Expect(conflict["error"].(map[string]any)["type"]).To(Equal("approval_stale"))

		saved := approveClient(s)
		Expect(saved["approved"]).To(BeTrue())
		Expect(saved["has_secret"]).To(BeTrue())
		Expect(saved["approved_by"]).To(Equal("root"))
		Expect(saved).To(HaveKey("approved_at"))

		env.changeDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Optional = append(d.Scopes.Optional, "admin") })
		read := decodeJSON(oauthDo(s, "root", http.MethodGet, "/api/chats/admin/oauth-clients/"+oauthAddonID, nil, nil))
		Expect(read["approved"]).To(BeFalse(), "a changed login ends the approval")
		Expect(read["current_digest"]).NotTo(Equal(digest))
	})

	It("never puts a secret, a token, a code, a state or a nonce in a response or a log", func() {
		var bodies []string
		keep := func(rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
			bodies = append(bodies, rec.Body.String(), rec.Header().Get("Location"))
			return rec
		}
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		l := startLogin(s, "alice", id, nil)
		keep(callback(s, "alice", url.Values{"state": {l.state}, "code": {"code-sentinel"}}, l.cookie))
		keep(oauthDo(s, "alice", http.MethodGet, "/api/chats/connectors", nil, nil))
		keep(oauthDo(s, "alice", http.MethodGet, "/api/chats/connectors/"+id, nil, nil))
		keep(oauthDo(s, "root", http.MethodGet, "/api/chats/admin/oauth-clients/"+oauthAddonID, nil, nil))
		keep(oauthDo(s, "root", http.MethodPut, "/api/chats/admin/oauth-clients/"+oauthAddonID, map[string]any{"client_id": "client-1", "descriptor_digest": "stale"}, nil))
		l2 := startLogin(s, "alice", id, nil)
		keep(callback(s, "bob", url.Values{"state": {l2.state}, "code": {"code-sentinel"}}, l2.cookie))
		l3 := startLogin(s, "alice", id, nil)
		keep(callback(s, "alice", url.Values{"state": {l3.state}, "error": {"server_error"}, "error_description": {"description-sentinel"}}, l3.cookie))
		keep(oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/disconnect", nil, nil))

		tokens := regexp.MustCompile(`(access|refresh)-[0-9]+`)
		for _, text := range append(bodies, env.logs.String()) {
			Expect(oauthtokentest.HasNoSentinel(text, oauthSecretV, "code-sentinel", l.state, l2.state, l3.state, l.cookie.Value, l2.cookie.Value, l3.cookie.Value)).To(BeTrue(), text)
			Expect(tokens.MatchString(text)).To(BeFalse(), text)
		}
		Expect(env.logs.String()).To(ContainSubstring("an OAuth login did not finish"), "the spec saw the log")
	})

	It("answers 429 too_many_logins when a binding has too many logins waiting, and sets no cookie", func() {
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		for range 5 {
			startLogin(s, "alice", id, nil)
		}
		rec := oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/start", nil, nil)
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests), rec.Body.String())
		Expect(decodeJSON(rec)["error"].(map[string]any)["type"]).To(Equal("too_many_logins"))
		Expect(nonceCookie(rec)).To(BeNil())
	})

	It("disconnects and says whether the service was told", func() {
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		l := startLogin(s, "alice", id, nil)
		location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie))
		rec := oauthDo(s, "bob", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/disconnect", nil, nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "another person's binding")
		rec = oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/disconnect", nil, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(Equal(map[string]any{"revoked": true}))
		Expect(binding(s, "alice", id)["oauth"].(map[string]any)["state"]).To(Equal("not_connected"))

		l = startLogin(s, "alice", id, nil)
		location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie))
		env.srv.Set(func(s *oauthtokentest.Server) { s.RevokeStatus = 503 })
		rec = oauthDo(s, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/disconnect", nil, nil)
		Expect(decodeJSON(rec)).To(Equal(map[string]any{"revoked": false}))
		Expect(binding(s, "alice", id)["oauth"].(map[string]any)["state"]).To(Equal("not_connected"), "removed here in any case")
	})

	It("revokes the login at the service when the connection is deleted, and deletes it when the service does not answer", func() {
		approveClient(s)
		id := createOAuthBinding(s, "alice")
		l := startLogin(s, "alice", id, nil)
		location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie))
		Expect(oauthDo(s, "alice", http.MethodDelete, "/api/chats/connectors/"+id, nil, nil).Code).To(Equal(http.StatusNoContent))
		Expect(env.srv.Revokes.Load()).To(BeEquivalentTo(1))
		Expect(env.srv.Forms()[len(env.srv.Forms())-1].Get("token_type_hint")).To(Equal("refresh_token"))

		id = createOAuthBinding(s, "alice")
		l = startLogin(s, "alice", id, nil)
		location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie))
		env.srv.Set(func(s *oauthtokentest.Server) { s.RevokeStatus = 503 })
		Expect(oauthDo(s, "alice", http.MethodDelete, "/api/chats/connectors/"+id, nil, nil).Code).To(Equal(http.StatusNoContent))
		Expect(oauthDo(s, "alice", http.MethodGet, "/api/chats/connectors/"+id, nil, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("answers a test of an OAuth connection with the classes of the login routes", func() {
		test := func(srv *studioServer, id string) (int, string) {
			rec := oauthDo(srv, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/test", nil, nil)
			if rec.Code == http.StatusOK {
				return rec.Code, ""
			}
			return rec.Code, decodeJSON(rec)["error"].(map[string]any)["type"].(string)
		}
		id := createOAuthBinding(s, "alice")
		code, typ := test(s, id)
		Expect([]any{code, typ}).To(Equal([]any{http.StatusConflict, "not_connected"}))
		approveClient(s)
		l := startLogin(s, "alice", id, nil)
		location(callback(s, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie))
		code, _ = test(s, id)
		Expect(code).To(Equal(http.StatusOK))

		env.srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "server_error", 503 })
		env.clock.Advance(2 * time.Hour)
		code, typ = test(s, id)
		Expect([]any{code, typ}).To(Equal([]any{http.StatusServiceUnavailable, "unavailable"}), "the service did not answer the refresh")

		env.srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "invalid_grant", 400 })
		code, typ = test(s, id)
		Expect([]any{code, typ}).To(Equal([]any{http.StatusConflict, "reconnect_needed"}))
		Expect(env.logs.String()).NotTo(ContainSubstring("the request failed"))
	})

	It("answers unavailable when no add-on gateway runs", func() {
		studioConnectorOptions = env.connectorOptions(true)
		studioRouteOptions = func(*studioServer) []studioendpoint.HandlerOption {
			return []studioendpoint.HandlerOption{studioendpoint.WithOAuth(studioendpoint.OAuthConfig{Mode: auth.AddonModeNamedUser, Dispatcher: func() studioendpoint.OAuthDispatcher { return nil }})}
		}
		off := newStudioServer()
		Expect(auth.UpdateUserPermissions(off.db, "alice", auth.PermissionMap{auth.FeatureConnectors: true, auth.FeatureAddons: true})).To(Succeed())
		off.e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error { c.Set("_external_base_url", oauthBase); return next(c) }
		})
		rec := oauthDo(off, "alice", http.MethodPost, "/api/chats/connectors/b1/oauth/start", nil, nil)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable), rec.Body.String())
		Expect(location(callback(off, "alice", url.Values{"state": {"s"}})).Query().Get("class")).To(Equal("unavailable"))
	})
})

var _ = Describe("OAuth routes with authentication off", func() {
	It("binds the login to the browser by the nonce cookie alone", func() {
		env := newOAuthEnv()
		studioConnectorOptions = env.connectorOptions(true)
		DeferCleanup(func() { studioConnectorOptions = nil })
		s := newStudioServer()
		ctx := context.Background()
		d, _ := env.descriptor(ctx, oauthAddonID)
		secret := oauthSecretV
		_, err := s.conns.SetClient(ctx, oauthAddonID, studio.OAuthClientInput{Digest: d.Digest(), ClientID: "client-1", Secret: &secret, ApprovedBy: "local-owner"})
		Expect(err).NotTo(HaveOccurred())
		b, err := s.conns.CreateConnector(ctx, "", studio.ConnectorInput{Kind: oauthKindID, Name: "Work", AuthKind: connectors.AuthOAuth2})
		Expect(err).NotTo(HaveOccurred())

		// No account database: no auth middleware, and the add-ons run as
		// the one local owner.
		gw := &gateway.Gateway{Revalidate: auth.AddonRevalidate(nil, auth.AddonModeLocalOwner), OAuthOwner: studio.OAuthOwner{
			Connectors: func() *studio.Connectors { return s.conns },
			Principal: func(a contract.Actor) string {
				if a.UserID == auth.AddonLocalOwnerID {
					return studio.AnonymousPrincipal
				}
				return ""
			},
		}}
		h := studioendpoint.New(s.svc, nil).Apply(studioendpoint.WithOAuth(studioendpoint.OAuthConfig{Mode: auth.AddonModeLocalOwner, Dispatcher: func() studioendpoint.OAuthDispatcher { return gw }}))
		e := echo.New()
		e.POST("/api/chats/connectors/:id/oauth/start", h.StartConnectorOAuth)
		e.GET("/api/chats/connectors/oauth/callback", h.ConnectorOAuthCallback)
		do := func(req *http.Request) *httptest.ResponseRecorder {
			req.Host = "localhost:8080"
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}
		start := func() login {
			rec := do(httptest.NewRequest(http.MethodPost, "/api/chats/connectors/"+b.ID+"/oauth/start", strings.NewReader(`{}`)))
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			u, _ := url.Parse(decodeJSON(rec)["authorize_url"].(string))
			ck := nonceCookie(rec)
			return login{authorize: u, state: u.Query().Get("state"), cookie: &http.Cookie{Name: ck.Name, Value: ck.Value}}
		}
		cb := func(state string, ck *http.Cookie) url.Values {
			req := httptest.NewRequest(http.MethodGet, "/api/chats/connectors/oauth/callback?"+url.Values{"state": {state}, "code": {"c"}}.Encode(), nil)
			if ck != nil {
				req.AddCookie(ck)
			}
			return location(do(req)).Query()
		}
		l := start()
		Expect(cb(l.state, nil).Get("class")).To(Equal("invalid_state"), "no cookie")
		l = start()
		Expect(cb(l.state, &http.Cookie{Name: l.cookie.Name, Value: "other"}).Get("class")).To(Equal("invalid_state"), "another browser")
		l = start()
		Expect(cb(l.state, l.cookie).Get("oauth")).To(Equal("ok"))
		list, err := s.conns.Connections(ctx, "", oauthAddonID)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(b.ID))
		Expect(list[0].State).To(Equal(studio.OAuthConnected))
	})
})

// oauthCarriers starts a real mTLS owner carrier and returns the carriers of
// the owner and the two frontends.
func oauthCarriers(handler func(owner *gateway.Carrier) http.Handler) map[string]*gateway.Carrier {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	Expect(err).NotTo(HaveOccurred())
	ca, err = x509.ParseCertificate(der)
	Expect(err).NotTo(HaveOccurred())
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	ids := []string{"owner", "front-a", "front-b"}
	certs := map[string]tls.Certificate{}
	pins := map[string][32]byte{}
	verify := map[string]ed25519.PublicKey{}
	signing := map[string]ed25519.PrivateKey{}
	for i, id := range ids {
		p, k, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: id}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, err := x509.CreateCertificate(rand.Reader, c, ca, p, key)
		Expect(err).NotTo(HaveOccurred())
		kd, err := x509.MarshalPKCS8PrivateKey(k)
		Expect(err).NotTo(HaveOccurred())
		pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
		Expect(err).NotTo(HaveOccurred())
		certs[id] = pair
		pins[id] = sha256.Sum256(pair.Certificate[0])
		sp, sk, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		verify[id], signing[id] = sp, sk
	}
	server := httptest.NewUnstartedServer(nil)
	out := map[string]*gateway.Carrier{}
	for _, id := range ids {
		c, err := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: signing[id], VerificationKeys: verify})
		Expect(err).NotTo(HaveOccurred())
		out[id] = c
	}
	server.Config.Handler = handler(out["owner"])
	server.TLS = out["owner"].ServerTLS()
	server.StartTLS()
	DeferCleanup(server.Close)
	return out
}

var _ = Describe("OAuth routes on two frontends", func() {
	It("finishes on the owner a login whose callback reached another frontend, which answers the redirect", func() {
		env := newOAuthEnv()
		db, err := auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		store, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		studioSharedDB, studioSharedConnStore = db, store
		DeferCleanup(func() { studioSharedDB, studioSharedConnStore = nil, nil })

		// A is the owner: it runs the add-ons and reads their descriptors.
		var ownerGW *gateway.Gateway
		a := env.build(true, func(*studioServer) *gateway.Gateway { return ownerGW })
		ownerGW = ownerGateway(a)
		carriers := oauthCarriers(func(owner *gateway.Carrier) http.Handler {
			h, err := owner.OwnerHandler(ownerGW)
			Expect(err).NotTo(HaveOccurred())
			return h
		})
		// B is a frontend: it has no descriptor and forwards every OAuth call.
		b := env.build(false, func(s *studioServer) *gateway.Gateway {
			return &gateway.Gateway{Forward: carriers["front-b"], Revalidate: auth.AddonRevalidate(s.db, auth.AddonModeNamedUser)}
		})
		_, err = b.conns.StartLogin(context.Background(), "alice", "any", nil, oauthCallback, "/app")
		Expect(err).To(HaveOccurred(), "the frontend cannot serve a login on its own")

		approveClient(b)
		id := createOAuthBinding(b, "alice")
		l := startLogin(a, "alice", id, nil)
		done := callback(b, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie)
		Expect(location(done).String()).To(Equal("/app/chats/connectors?oauth=ok"))
		Expect(nonceCookie(done).MaxAge).To(BeNumerically("<", 0))
		Expect(env.srv.Tokens.Load()).To(BeEquivalentTo(1), "the owner traded the code once")
		for _, s := range []*studioServer{a, b} {
			Expect(binding(s, "alice", id)["oauth"].(map[string]any)["state"]).To(Equal("connected"))
		}

		// A start on B and a replay of its state on A.
		l = startLogin(b, "alice", id, nil)
		Expect(location(callback(a, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie)).Query().Get("oauth")).To(Equal("ok"))
		Expect(location(callback(b, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie)).Query().Get("class")).To(Equal("invalid_state"))

		// The frontend cannot read the descriptor, so its test of the login
		// is unavailable, not a failure of the server.
		rec := oauthDo(b, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/test", nil, nil)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable), rec.Body.String())
		Expect(decodeJSON(rec)["error"].(map[string]any)["type"]).To(Equal("unavailable"))

		rec = oauthDo(b, "alice", http.MethodPost, "/api/chats/connectors/"+id+"/oauth/disconnect", nil, nil)
		Expect(decodeJSON(rec)).To(Equal(map[string]any{"revoked": true}))
		Expect(binding(a, "alice", id)["oauth"].(map[string]any)["state"]).To(Equal("not_connected"))

		// Deleting the connection on the frontend revokes its login on the
		// owner first.
		l = startLogin(b, "alice", id, nil)
		location(callback(b, "alice", url.Values{"state": {l.state}, "code": {"c"}}, l.cookie))
		revokes := env.srv.Revokes.Load()
		Expect(oauthDo(b, "alice", http.MethodDelete, "/api/chats/connectors/"+id, nil, nil).Code).To(Equal(http.StatusNoContent))
		Expect(env.srv.Revokes.Load()).To(Equal(revokes + 1))
		Expect(oauthDo(a, "alice", http.MethodGet, "/api/chats/connectors/"+id, nil, nil).Code).To(Equal(http.StatusNotFound))
	})
})
