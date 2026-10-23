package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/httpclient"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type oauth2TestRoundTripper func(*http.Request) (*http.Response, error)

func (f oauth2TestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// fakeIdP is a minimal OAuth2 token endpoint for the client_credentials grant.
type fakeIdP struct {
	server    *httptest.Server
	requests  atomic.Int32
	failing   atomic.Bool
	expiresIn int
	clientID  string
	secret    string

	mu         sync.Mutex
	lastScope  string
	lastParams map[string]string
}

func newFakeIdP(clientID, secret string, expiresIn int) *fakeIdP {
	idp := &fakeIdP{clientID: clientID, secret: secret, expiresIn: expiresIn}
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := idp.requests.Add(1)
		if idp.failing.Load() {
			http.Error(w, "idp down", http.StatusServiceUnavailable)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, sec, ok := r.BasicAuth()
		if !ok {
			id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if r.PostForm.Get("grant_type") != "client_credentials" || id != idp.clientID || sec != idp.secret {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		idp.mu.Lock()
		idp.lastScope = r.PostForm.Get("scope")
		idp.lastParams = map[string]string{"audience": r.PostForm.Get("audience")}
		idp.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("tok-%d", n),
			"token_type":   "Bearer",
			"expires_in":   idp.expiresIn,
		})
	}))
	DeferCleanup(idp.server.Close)
	return idp
}

// authRecorder stands in for an MCP server behind an OIDC-protected proxy: it
// records every Authorization header it receives.
type authRecorder struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []string
}

func newAuthRecorder() *authRecorder {
	rec := &authRecorder{}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.seen = append(rec.seen, r.Header.Get("Authorization"))
		rec.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	DeferCleanup(rec.server.Close)
	return rec
}

func (r *authRecorder) headers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func doGet(client *http.Client, url string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// protectedMCPServer serves a real MCP server (streamable HTTP or SSE) that
// rejects every request not carrying "Bearer <want()>".
func protectedMCPServer(sse bool, want func() string) *httptest.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "protected", Version: "v0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "whoami", Description: "test tool"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	var handler http.Handler
	if sse {
		handler = mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	} else {
		handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want() {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	DeferCleanup(ts.Close)
	return ts
}

var _ = Describe("OAuth2 client_credentials for remote MCP servers", func() {
	var (
		idp   *fakeIdP
		rec   *authRecorder
		clock *fakeClock
	)

	BeforeEach(func() {
		idp = newFakeIdP("localai", "s3cret", 100)
		rec = newAuthRecorder()
		clock = &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	})

	clientFor := func(cfg config.MCPOAuth2Config) *http.Client {
		rt, err := newOAuth2ClientCredentialsRoundTripper(cfg, httpclient.HardenedTransport(), clock.Now)
		Expect(err).NotTo(HaveOccurred())
		return httpclient.New(httpclient.WithTransport(rt))
	}

	baseCfg := func() config.MCPOAuth2Config {
		return config.MCPOAuth2Config{
			TokenURL:       idp.server.URL,
			ClientID:       "localai",
			ClientSecret:   "s3cret",
			Scopes:         []string{"mcp:tools"},
			EndpointParams: map[string]string{"audience": "mcp-gateway"},
		}
	}

	It("fetches a token and sends it as a bearer token, with scopes and endpoint params", func() {
		client := clientFor(baseCfg())
		Expect(doGet(client, rec.server.URL)).To(Succeed())

		Expect(rec.headers()).To(Equal([]string{"Bearer tok-1"}))
		idp.mu.Lock()
		defer idp.mu.Unlock()
		Expect(idp.lastScope).To(Equal("mcp:tools"))
		Expect(idp.lastParams["audience"]).To(Equal("mcp-gateway"))
	})

	It("caches the token across requests", func() {
		client := clientFor(baseCfg())
		for range 5 {
			Expect(doGet(client, rec.server.URL)).To(Succeed())
		}
		Expect(idp.requests.Load()).To(BeEquivalentTo(1))
		Expect(rec.headers()).To(HaveEach("Bearer tok-1"))
	})

	It("refreshes proactively at half of the token lifetime", func() {
		client := clientFor(baseCfg())
		Expect(doGet(client, rec.server.URL)).To(Succeed())

		clock.Advance(49 * time.Second)
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(idp.requests.Load()).To(BeEquivalentTo(1), "before half-life the cached token is reused")

		clock.Advance(2 * time.Second) // 51s of a 100s token
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(idp.requests.Load()).To(BeEquivalentTo(2))
		Expect(rec.headers()).To(Equal([]string{"Bearer tok-1", "Bearer tok-1", "Bearer tok-2"}))
	})

	It("keeps the old token while refresh fails, then fails loudly near expiry without an unauthenticated request", func() {
		client := clientFor(baseCfg())
		Expect(doGet(client, rec.server.URL)).To(Succeed())

		idp.failing.Store(true)
		clock.Advance(51 * time.Second)
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(idp.requests.Load()).To(BeEquivalentTo(2), "a refresh was attempted")

		// Retries are throttled while the IdP is down.
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(idp.requests.Load()).To(BeEquivalentTo(2))

		clock.Advance(18 * time.Second) // t=69s, expiry 100s, margin 30s
		Expect(doGet(client, rec.server.URL)).To(Succeed())

		clock.Advance(2 * time.Second) // t=71s: inside the safety margin
		err := doGet(client, rec.server.URL)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("oauth2"))

		Expect(rec.headers()).To(HaveEach("Bearer tok-1"), "no request may go out without a token")
		Expect(rec.headers()).To(HaveLen(4))

		// Counter-check: once the IdP recovers, requests succeed again.
		idp.failing.Store(false)
		clock.Advance(oauth2RetryInterval)
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(rec.headers()[len(rec.headers())-1]).To(MatchRegexp(`^Bearer tok-\d+$`))
		Expect(rec.headers()[len(rec.headers())-1]).NotTo(Equal("Bearer tok-1"))
	})

	It("rechecks token expiry after a slow failed refresh", func() {
		client := clientFor(baseCfg())
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		rt := client.Transport.(*oauth2ClientCredentialsRoundTripper)
		base := rt.tokenClient.Transport
		rt.tokenClient.Transport = oauth2TestRoundTripper(func(r *http.Request) (*http.Response, error) {
			clock.Advance(20 * time.Second)
			return base.RoundTrip(r)
		})
		idp.failing.Store(true)
		clock.Advance(51 * time.Second)

		Expect(doGet(client, rec.server.URL)).NotTo(Succeed())
		Expect(rec.headers()).To(Equal([]string{"Bearer tok-1"}), "the failed fetch crossed the expiry safety margin")
	})

	It("starts the retry interval when a slow failed refresh finishes", func() {
		client := clientFor(baseCfg())
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		rt := client.Transport.(*oauth2ClientCredentialsRoundTripper)
		base := rt.tokenClient.Transport
		rt.tokenClient.Transport = oauth2TestRoundTripper(func(r *http.Request) (*http.Response, error) {
			clock.Advance(10 * time.Second)
			return base.RoundTrip(r)
		})
		idp.failing.Store(true)
		clock.Advance(51 * time.Second)

		Expect(doGet(client, rec.server.URL)).To(Succeed())
		attempts := idp.requests.Load()
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(idp.requests.Load()).To(Equal(attempts))
	})

	It("throttles failed fetches before any token is cached", func() {
		client := clientFor(baseCfg())
		idp.failing.Store(true)
		Expect(doGet(client, rec.server.URL)).NotTo(Succeed())
		attempts := idp.requests.Load()
		Expect(attempts).To(BeNumerically(">", 0))

		Expect(doGet(client, rec.server.URL)).NotTo(Succeed())
		Expect(idp.requests.Load()).To(Equal(attempts))
		Expect(rec.headers()).To(BeEmpty())

		idp.failing.Store(false)
		clock.Advance(oauth2RetryInterval)
		Expect(doGet(client, rec.server.URL)).To(Succeed())
		Expect(rec.headers()).To(HaveLen(1))
	})

	It("errors instead of sending an unauthenticated request when the first fetch fails", func() {
		cfg := baseCfg()
		cfg.ClientSecret = "wrong"
		client := clientFor(cfg)
		Expect(doGet(client, rec.server.URL)).NotTo(Succeed())
		Expect(rec.headers()).To(BeEmpty())
	})

	It("reads client id and secret from environment variables", func() {
		Expect(os.Setenv("TEST_MCP_OAUTH_ID", "localai")).To(Succeed())
		Expect(os.Setenv("TEST_MCP_OAUTH_SECRET", "s3cret")).To(Succeed())
		DeferCleanup(func() {
			_ = os.Unsetenv("TEST_MCP_OAUTH_ID")
			_ = os.Unsetenv("TEST_MCP_OAUTH_SECRET")
		})
		cfg := config.MCPOAuth2Config{
			TokenURL:        idp.server.URL,
			ClientIDEnv:     "TEST_MCP_OAUTH_ID",
			ClientSecretEnv: "TEST_MCP_OAUTH_SECRET",
		}
		Expect(doGet(clientFor(cfg), rec.server.URL)).To(Succeed())
		Expect(rec.headers()).To(Equal([]string{"Bearer tok-1"}))
	})

	It("refuses to build the transport when a referenced environment variable is unset", func() {
		_ = os.Unsetenv("TEST_MCP_OAUTH_MISSING")
		_, err := newRemoteServerTransport(config.MCPRemoteServer{
			URL: rec.server.URL,
			OAuth2: &config.MCPOAuth2Config{
				TokenURL:        idp.server.URL,
				ClientID:        "localai",
				ClientSecretEnv: "TEST_MCP_OAUTH_MISSING",
			},
		}, nil)
		Expect(err).To(MatchError(ContainSubstring("TEST_MCP_OAUTH_MISSING")))
	})

	It("keeps the static token path unchanged and never contacts a token endpoint", func() {
		rt, err := newRemoteServerTransport(config.MCPRemoteServer{URL: rec.server.URL, Token: "static"}, httpclient.HardenedTransport())
		Expect(err).NotTo(HaveOccurred())
		Expect(doGet(httpclient.New(httpclient.WithTransport(rt)), rec.server.URL)).To(Succeed())

		rt, err = newRemoteServerTransport(config.MCPRemoteServer{URL: rec.server.URL}, httpclient.HardenedTransport())
		Expect(err).NotTo(HaveOccurred())
		Expect(doGet(httpclient.New(httpclient.WithTransport(rt)), rec.server.URL)).To(Succeed())

		Expect(rec.headers()).To(Equal([]string{"Bearer static", ""}))
		Expect(idp.requests.Load()).To(BeZero())
	})

	It("rejects a static token combined with oauth2", func() {
		_, err := newRemoteServerTransport(config.MCPRemoteServer{
			URL:    rec.server.URL,
			Token:  "static",
			OAuth2: &config.MCPOAuth2Config{TokenURL: idp.server.URL, ClientID: "a", ClientSecret: "b"},
		}, nil)
		Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))
	})

	Context("against a real MCP server behind a token check", func() {
		It("establishes a streamable-HTTP session through NamedSessionsFromMCPConfig", func() {
			ts := protectedMCPServer(false, func() string { return "tok-1" })
			modelName := "oauth2-mcp-streamable-test"
			DeferCleanup(func() { CloseMCPSessions(modelName) })

			remote := config.MCPGenericConfig[config.MCPRemoteServers]{
				Servers: config.MCPRemoteServers{
					"protected": {URL: ts.URL, OAuth2: &config.MCPOAuth2Config{
						TokenURL: idp.server.URL, ClientID: "localai", ClientSecret: "s3cret",
					}},
				},
			}
			sessions, err := NamedSessionsFromMCPConfig(modelName, remote, config.MCPGenericConfig[config.MCPSTDIOServers]{}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].Error).To(BeEmpty())
			Expect(sessions[0].Session).NotTo(BeNil())

			tools, err := DiscoverMCPTools(context.Background(), sessions)
			Expect(err).NotTo(HaveOccurred())
			Expect(tools).To(HaveLen(1))
			Expect(tools[0].ToolName).To(Equal("whoami"))
			Expect(idp.requests.Load()).To(BeEquivalentTo(1))
		})

		It("counter-check: the same server refuses the session without oauth2", func() {
			ts := protectedMCPServer(false, func() string { return "tok-1" })
			modelName := "oauth2-mcp-streamable-negative-test"
			DeferCleanup(func() { CloseMCPSessions(modelName) })

			remote := config.MCPGenericConfig[config.MCPRemoteServers]{
				Servers: config.MCPRemoteServers{"protected": {URL: ts.URL}},
			}
			sessions, err := NamedSessionsFromMCPConfig(modelName, remote, config.MCPGenericConfig[config.MCPSTDIOServers]{}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].Session).To(BeNil())
			Expect(sessions[0].Error).To(ContainSubstring("connection failed"))
		})

		It("also authenticates the SSE transport", func() {
			ts := protectedMCPServer(true, func() string { return "tok-1" })
			rt, err := newRemoteServerTransport(config.MCPRemoteServer{
				URL: ts.URL,
				OAuth2: &config.MCPOAuth2Config{
					TokenURL: idp.server.URL, ClientID: "localai", ClientSecret: "s3cret",
				},
			}, httpclient.HardenedTransport())
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
			session, err := client.Connect(ctx, &mcp.SSEClientTransport{
				Endpoint:   ts.URL,
				HTTPClient: httpclient.New(httpclient.WithTransport(rt)),
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = session.Close() })

			res, err := session.ListTools(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Tools).To(HaveLen(1))
			Expect(idp.requests.Load()).To(BeEquivalentTo(1))
		})
	})
})
