package mcpconns_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// TestMain lets the test binary act as a stdio MCP server.
func TestMain(m *testing.M) {
	if os.Getenv("MCPCONNS_HELPER") == "1" {
		srv := mcp.NewServer(&mcp.Implementation{Name: "helper", Version: "1"}, nil)
		addEcho(srv, "local_echo")
		_ = srv.Run(context.Background(), &mcp.StdioTransport{})
		return
	}
	os.Exit(m.Run())
}

type echoIn struct {
	Text string `json:"text"`
}

func addEcho(srv *mcp.Server, name string) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "echoes"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}}, nil, nil
	})
}

// fakeServer is an MCP server over streamable HTTP that records what it saw.
type fakeServer struct {
	*httptest.Server
	inits   atomic.Int32
	mu      sync.Mutex
	auths   []string
	headers []string
}

func newFakeServer(slow time.Duration) *fakeServer {
	f := &fakeServer{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	addEcho(srv, "echo")
	mcp.AddTool(srv, &mcp.Tool{Name: "slow", Description: "sleeps"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ echoIn) (*mcp.CallToolResult, any, error) {
		select {
		case <-time.After(slow):
		case <-ctx.Done():
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "spawn_agent", Description: "tries a reserved name"}, func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.headers = append(f.headers, r.Header.Get("X-Team"))
		f.mu.Unlock()
		if r.Method == http.MethodPost {
			var b bytes.Buffer
			_, _ = b.ReadFrom(r.Body)
			if strings.Contains(b.String(), `"method":"initialize"`) {
				f.inits.Add(1)
			}
			r.Body = nopCloser{bytes.NewReader(b.Bytes())}
		}
		h.ServeHTTP(w, r)
	}))
	DeferCleanup(func() { f.CloseClientConnections(); f.Close() })
	return f
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func (f *fakeServer) sawAuth(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.auths, want)
}

type env struct {
	svc     *mcpconns.Service
	store   mcpconns.Store
	now     time.Time
	permit  atomic.Bool
	admin   atomic.Bool
	private atomic.Bool
	mu      sync.Mutex
}

func newEnv(mutate ...func(*mcpconns.Options)) *env {
	e := &env{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	e.permit.Store(true)
	e.private.Store(true) // the test servers listen on loopback
	store, err := mcpconns.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	e.store = store
	key, _ := connectors.NewKey()
	o := mcpconns.Options{
		Store:          store,
		Key:            func(context.Context) ([]byte, error) { return key, nil },
		Permitted:      func(context.Context, string) bool { return e.permit.Load() },
		IsAdmin:        func(context.Context, string) bool { return e.admin.Load() },
		AllowPrivate:   func() bool { return e.private.Load() },
		Clock:          func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now },
		ConnectTimeout: 5 * time.Second,
		CallTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}
	for _, m := range mutate {
		m(&o)
	}
	svc, err := mcpconns.New(o)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(svc.Close)
	e.svc = svc
	return e
}

func (e *env) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }

// specCtx returns a context that ends with the spec.
func specCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	return ctx
}

func str(s string) *string { return &s }

func httpInput(name, url string) mcpconns.Input {
	return mcpconns.Input{Name: name, Transport: mcpconns.TransportHTTP, URL: url, Enabled: true}
}

var _ = Describe("Service", func() {
	It("seals secrets on create and never returns them", func() {
		e := newEnv()
		ctx := specCtx()
		hdr := map[string]string{"X-Team": "blue-team-secret"}
		in := httpInput("Docs", "https://mcp.example/mcp")
		in.Token, in.Headers = str("tok-123456"), &hdr
		c, err := e.svc.Create(ctx, "u1", in)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.HasToken).To(BeTrue(), "the record misses a flag")
		Expect(c.HasHeaders).To(BeTrue(), "the record misses a flag")
		Expect(c.HasEnv).To(BeFalse())
		Expect(c.Secrets).To(BeNil(), "the record leaks the secrets")
		raw, _ := e.store.Get(ctx, c.ID)
		Expect(raw.Secrets).NotTo(BeEmpty(), "the secrets are not sealed in the store")
		Expect(raw.Secrets).NotTo(ContainSubstring("tok-123456"), "the secrets are not sealed in the store")
		Expect(raw.Secrets).NotTo(ContainSubstring("blue-team-secret"), "the secrets are not sealed in the store")
		j, _ := json.Marshal(c)
		Expect(string(j)).NotTo(ContainSubstring("tok-123456"), "the token is in the JSON of a connector")
		// A sealed value cannot be moved to another connector or owner.
		other := raw
		other.ID, other.Name = "stolen", "Stolen"
		_ = e.store.Put(ctx, other)
		p, err := e.svc.Test(ctx, "u1", "stolen")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Error).NotTo(BeEmpty(), "a sealed value that was moved must not open")
	})

	It("keeps secrets on update unless given and can clear them", func() {
		e := newEnv()
		ctx := specCtx()
		in := httpInput("Docs", "https://mcp.example/mcp")
		in.Token = str("tok")
		c, _ := e.svc.Create(ctx, "u1", in)
		up := httpInput("Docs renamed", "https://mcp.example/mcp")
		got, err := e.svc.Update(ctx, "u1", c.ID, up)
		Expect(err).NotTo(HaveOccurred(), "an update without a token must keep it")
		Expect(got.HasToken).To(BeTrue(), "an update without a token must keep it")
		Expect(got.Name).To(Equal("Docs renamed"))
		up.Token = str("")
		got, _ = e.svc.Update(ctx, "u1", c.ID, up)
		Expect(got.HasToken).To(BeFalse(), "an empty token must clear it")
	})

	It("never lets another user see, edit, test or use a connector", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, err := e.svc.Create(ctx, "u1", httpInput("Mine", f.URL))
		Expect(err).NotTo(HaveOccurred())
		l, _ := e.svc.List(ctx, "u2")
		Expect(l).To(BeEmpty())
		_, err = e.svc.Get(ctx, "u2", c.ID)
		Expect(err).To(MatchError(mcpconns.ErrNotFound), "get")
		_, err = e.svc.Update(ctx, "u2", c.ID, httpInput("Hijack", f.URL))
		Expect(err).To(MatchError(mcpconns.ErrNotFound), "update")
		_, err = e.svc.SetEnabled(ctx, "u2", c.ID, false)
		Expect(err).To(MatchError(mcpconns.ErrNotFound), "enable")
		_, err = e.svc.Test(ctx, "u2", c.ID)
		Expect(err).To(MatchError(mcpconns.ErrNotFound), "test")
		Expect(e.svc.Delete(ctx, "u2", c.ID)).To(MatchError(mcpconns.ErrNotFound), "delete")
		tools, missing := e.svc.Resolve(ctx, "u2", []string{c.ID}, false)
		Expect(tools).To(BeEmpty(), "u2 used the connector of u1")
		Expect(missing).To(HaveLen(1), "u2 used the connector of u1")
		allTools, _ := e.svc.Resolve(ctx, "u2", nil, true)
		Expect(allTools).To(BeEmpty(), "u2 got the connectors of u1 in all-mode")
		got, _ := e.svc.Get(ctx, "u1", c.ID)
		Expect(got.Name).To(Equal("Mine"), "the owner's connector was changed")
		Expect(strings.Contains(strings.Join(missing, " "), "could not be used") || missing[0] != "").To(BeTrue(), "missing = %v", missing)
	})

	It("checks the permission on every use", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Mine", f.URL))
		withPermission, _ := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(withPermission).NotTo(BeEmpty(), "no tools with the permission")
		e.permit.Store(false)
		_, err := e.svc.Create(ctx, "u1", httpInput("Other", f.URL))
		Expect(err).To(MatchError(mcpconns.ErrForbidden), "create without the feature")
		_, err = e.svc.Test(ctx, "u1", c.ID)
		Expect(err).To(MatchError(mcpconns.ErrForbidden), "test without the feature")
		tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(tools).To(BeEmpty(), "a revoked feature still gave tools")
		Expect(missing).To(HaveLen(1), "a revoked feature still gave tools")
		allTools, _ := e.svc.Resolve(ctx, "u1", nil, true)
		Expect(allTools).To(BeEmpty(), "all-mode gave tools without the feature")
	})

	It("refuses invalid input", func() {
		e := newEnv()
		e.private.Store(false)
		ctx := specCtx()
		hdr := func(k, v string) *map[string]string { m := map[string]string{k: v}; return &m }
		cases := map[string]mcpconns.Input{
			"ftp":            httpInput("a", "ftp://mcp.example"),
			"no host":        httpInput("a", "https://"),
			"userinfo":       httpInput("a", "https://user:pw@mcp.example"),
			"loopback":       httpInput("a", "http://127.0.0.1:9000/mcp"),
			"private net":    httpInput("a", "http://10.1.2.3/mcp"),
			"metadata":       httpInput("a", "http://169.254.169.254/latest"),
			"localhost":      httpInput("a", "http://localhost:9000"),
			"unspecified":    httpInput("a", "http://0.0.0.0/"),
			"ipv6 loopback":  httpInput("a", "http://[::1]:9000/"),
			"empty name":     httpInput("  ", "https://mcp.example"),
			"bad transport":  {Name: "a", Transport: "ws", URL: "https://mcp.example"},
			"header newline": {Name: "a", Transport: "http", URL: "https://mcp.example", Headers: hdr("X-A", "v\r\nX-B: 1")},
			"header name":    {Name: "a", Transport: "http", URL: "https://mcp.example", Headers: hdr("Bad Name", "v")},
			"host header":    {Name: "a", Transport: "http", URL: "https://mcp.example", Headers: hdr("Host", "evil")},
			"http with cmd":  {Name: "a", Transport: "http", URL: "https://mcp.example", Command: "ls"},
		}
		for name, in := range cases {
			in.Enabled = true
			_, err := e.svc.Create(ctx, "u1", in)
			Expect(err).To(MatchError(mcpconns.ErrInvalid), name)
		}
		// A disabled connector with a private address may be saved (an import or a
		// conversion makes them), but cannot be switched on.
		off := httpInput("Internal", "http://10.1.2.3/mcp")
		off.Enabled = false
		c, err := e.svc.Create(ctx, "u1", off)
		Expect(err).NotTo(HaveOccurred(), "a disabled connector with a private address")
		_, err = e.svc.SetEnabled(ctx, "u1", c.ID, true)
		Expect(err).To(MatchError(mcpconns.ErrInvalid), "enabling a private address")
		e.private.Store(true)
		_, err = e.svc.SetEnabled(ctx, "u1", c.ID, true)
		Expect(err).NotTo(HaveOccurred(), "enabling once the administrator allows it")
		// Names are unique per user.
		_, err = e.svc.Create(ctx, "u1", httpInput("Internal", "https://mcp.example"))
		Expect(err).To(MatchError(mcpconns.ErrInvalid), "duplicate name")
		_, err = e.svc.Create(ctx, "u2", httpInput("Internal", "https://mcp.example"))
		Expect(err).NotTo(HaveOccurred(), "the same name for another user")
	})

	It("allows at most the per-user limit", func() {
		e := newEnv()
		ctx := specCtx()
		for i := range mcpconns.MaxPerUser {
			in := httpInput("c"+string(rune('a'+i)), "https://mcp.example")
			_, err := e.svc.Create(ctx, "u1", in)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := e.svc.Create(ctx, "u1", httpInput("one more", "https://mcp.example"))
		Expect(err).To(MatchError(mcpconns.ErrInvalid), "over the limit")
	})

	It("lists tools on a connection test and sends the secrets only to the server", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		hdr := map[string]string{"X-Team": "blue"}
		in := httpInput("Docs", f.URL)
		in.Token, in.Headers = str("tok-abcdef"), &hdr
		in.Enabled = false
		c, _ := e.svc.Create(ctx, "u1", in)
		p, err := e.svc.Test(ctx, "u1", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Error).To(BeEmpty())
		Expect(strings.Join(p.Tools, ",")).To(Equal("echo,slow,spawn_agent"))
		Expect(p.Details).To(Equal([]mcpconns.ToolInfo{{Name: "echo", Description: "echoes"}, {Name: "slow", Description: "sleeps"}, {Name: "spawn_agent", Description: "tries a reserved name"}}))
		Expect(f.sawAuth("Bearer tok-abcdef")).To(BeTrue(), "the server did not get the bearer token")
		f.mu.Lock()
		seen := strings.Join(f.headers, ",")
		f.mu.Unlock()
		Expect(seen).To(ContainSubstring("blue"), "the server did not get the custom header")
	})

	It("gives an error that holds no secret when the server is unreachable", func() {
		e := newEnv()
		ctx := specCtx()
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		in := httpInput("Dead", url)
		in.Token = str("tok-supersecret")
		c, _ := e.svc.Create(ctx, "u1", in)
		p, err := e.svc.Test(ctx, "u1", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Error).NotTo(BeEmpty(), "probe of a dead server")
		Expect(p.Tools).To(BeEmpty(), "probe of a dead server")
		Expect(p.Error).NotTo(ContainSubstring("tok-supersecret"), "the error echoes the token")
		tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(tools).To(BeEmpty(), "an unreachable connector must be a note")
		Expect(missing).To(HaveLen(1), "an unreachable connector must be a note")
		Expect(missing[0]).To(ContainSubstring("Dead"))
	})

	It("refuses private addresses when the connection is made", func() {
		e := newEnv()
		e.private.Store(false)
		ctx := specCtx()
		f := newFakeServer(0)
		// The record was saved when private addresses were allowed (or by another
		// route); the name resolves to loopback when the dial happens.
		port := f.URL[strings.LastIndex(f.URL, ":"):]
		for name, url := range map[string]string{"by name": "http://localhost" + port, "by address": f.URL, "ipv6": "http://[::1]" + port} {
			_ = e.store.Put(ctx, mcpconns.Connector{ID: "x-" + name, Owner: "u1", Name: name, Transport: mcpconns.TransportHTTP, URL: url, Enabled: true})
			p, err := e.svc.Test(ctx, "u1", "x-"+name)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(p.Error).NotTo(BeEmpty(), name)
			Expect(p.Tools).To(BeEmpty(), name)
			Expect(p.Error).NotTo(ContainSubstring("mcpconns:"), "%s: the reason shows the package prefix", name)
			tools, missing := e.svc.Resolve(ctx, "u1", []string{"x-" + name}, false)
			Expect(tools).To(BeEmpty(), name)
			Expect(missing).To(HaveLen(1), name)
		}
		Expect(f.inits.Load()).To(BeZero(), "the server on loopback was reached")
		e.private.Store(true)
		p, _ := e.svc.Test(ctx, "u1", "x-by address")
		Expect(p.Error).To(BeEmpty(), "with the administrator's switch on")
	})

	It("refuses a redirect to an internal address", func() {
		e := newEnv()
		ctx := specCtx()
		target := newFakeServer(0)
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		}))
		DeferCleanup(redirect.Close)
		e.private.Store(true)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Redirecting", redirect.URL))
		p, _ := e.svc.Test(ctx, "u1", c.ID)
		Expect(p.Error).NotTo(BeEmpty(), "a redirect was followed")
		Expect(target.inits.Load()).To(BeZero(), "the redirect target was reached")
	})

	It("resolves tools that run and caps nothing it does not own", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		off := httpInput("Off", f.URL)
		off.Enabled = false
		d, _ := e.svc.Create(ctx, "u1", off)

		tools, missing := e.svc.Resolve(ctx, "u1", nil, true)
		Expect(missing).To(BeEmpty())
		var echo *mcpconns.Tool
		for i := range tools {
			if tools[i].Name == "echo" {
				echo = &tools[i]
			}
			Expect(tools[i].ConnectorID).To(Equal(c.ID), "tool of the wrong connector")
			Expect(tools[i].Connector).To(Equal("Docs"), "tool of the wrong connector")
		}
		Expect(echo).NotTo(BeNil(), "no echo tool")
		out, err := echo.Run(ctx, json.RawMessage(`{"text":"hi"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("echo: hi"))
		// A disabled connector named by id is a note, never a failure.
		tools, missing = e.svc.Resolve(ctx, "u1", []string{d.ID, "gone"}, false)
		Expect(tools).To(BeEmpty(), "disabled or deleted")
		Expect(missing).To(HaveLen(2), "disabled or deleted")
	})

	It("times out a call that takes too long", func() {
		e := newEnv(func(o *mcpconns.Options) { o.CallTimeout = 150 * time.Millisecond })
		ctx := specCtx()
		f := newFakeServer(5 * time.Second)
		_, _ = e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		tools, _ := e.svc.Resolve(ctx, "u1", nil, true)
		for _, tl := range tools {
			if tl.Name == "slow" {
				start := time.Now()
				_, err := tl.Run(ctx, json.RawMessage(`{"text":"x"}`))
				Expect(err).To(HaveOccurred(), "slow call")
				Expect(time.Since(start)).To(BeNumerically("<=", 3*time.Second), "slow call")
				return
			}
		}
		Fail("no slow tool")
	})

	It("caches sessions per connector and closes them on edit, disable and delete", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		resolve := func() int {
			tools, _ := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
			return len(tools)
		}
		// Call twice: with || the second call ran only when the first found none,
		// so the cache was never asked.
		first, second := resolve(), resolve()
		Expect(first).NotTo(BeZero(), "no tools")
		Expect(second).NotTo(BeZero(), "no tools")
		Expect(f.inits.Load()).To(Equal(int32(1)), "two resolves must open one session")
		Expect(e.svc.Sessions()).To(Equal(1))
		_, err := e.svc.Update(ctx, "u1", c.ID, httpInput("Docs", f.URL))
		Expect(err).NotTo(HaveOccurred())
		Expect(e.svc.Sessions()).To(BeZero(), "an edit left the session open")
		resolve()
		Expect(f.inits.Load()).To(Equal(int32(2)), "after an edit the next use must reconnect")
		_, _ = e.svc.SetEnabled(ctx, "u1", c.ID, false)
		Expect(e.svc.Sessions()).To(BeZero(), "disabling left the session open")
		_, _ = e.svc.SetEnabled(ctx, "u1", c.ID, true)
		resolve()
		Expect(e.svc.Delete(ctx, "u1", c.ID)).To(Succeed())
		Expect(e.svc.Sessions()).To(BeZero(), "a delete left the session open")
	})

	It("notices an edit on another frontend by the version", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		// Another frontend writes the shared record without telling this one.
		raw, _ := e.store.Get(ctx, c.ID)
		raw.Updated = raw.Updated.Add(time.Minute)
		_ = e.store.Put(ctx, raw)
		e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(f.inits.Load()).To(Equal(int32(2)), "a changed record must reconnect")
	})

	It("closes idle sessions", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		e.advance(30 * time.Second)
		e.svc.ReapIdle()
		Expect(e.svc.Sessions()).To(Equal(1), "a session in use was reaped")
		e.advance(2 * time.Minute)
		e.svc.ReapIdle()
		Expect(e.svc.Sessions()).To(BeZero(), "an idle session stayed")
	})

	It("needs an administrator for stdio, refuses it in a cluster and runs it without the server environment", func() {
		ctx := specCtx()
		self, err := os.Executable()
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv("LOCALAI_TEST_SERVER_SECRET", "do-not-leak")
		env := map[string]string{"MCPCONNS_HELPER": "1"}
		in := mcpconns.Input{Name: "Local", Transport: mcpconns.TransportStdio, Command: self, Env: &env, Enabled: true}

		e := newEnv()
		_, err = e.svc.Create(ctx, "u1", in)
		Expect(err).To(MatchError(mcpconns.ErrForbidden), "a user who is not an administrator")
		e.admin.Store(true)
		c, err := e.svc.Create(ctx, "u1", in)
		Expect(err).NotTo(HaveOccurred())
		p, err := e.svc.Test(ctx, "u1", c.ID)
		Expect(err).NotTo(HaveOccurred(), "stdio probe")
		Expect(p.Error).To(BeEmpty(), "stdio probe")
		Expect(strings.Join(p.Tools, ",")).To(Equal("local_echo"))
		// Demoted: the connector stays but cannot be used.
		e.admin.Store(false)
		tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(tools).To(BeEmpty(), "a demoted owner still ran a command")
		Expect(missing).To(HaveLen(1), "a demoted owner still ran a command")

		cluster := newEnv(func(o *mcpconns.Options) { o.Distributed = true })
		cluster.admin.Store(true)
		_, err = cluster.svc.Create(ctx, "u1", in)
		Expect(err).To(MatchError(mcpconns.ErrInvalid), "stdio in a cluster")
		Expect(err.Error()).To(ContainSubstring("distributed"))
	})

	It("builds a stdio environment that is minimal plus the sealed one", func() {
		got := mcpconns.StdioEnv(map[string]string{"A": "1"}, []string{"PATH=/bin", "HOME=/h", "LOCALAI_API_KEY=secret", "AWS_SECRET_ACCESS_KEY=x"})
		joined := strings.Join(got, "\n")
		Expect(joined).NotTo(ContainSubstring("secret"))
		Expect(joined).NotTo(ContainSubstring("AWS"))
		Expect(joined).To(ContainSubstring("PATH=/bin"))
		Expect(joined).To(ContainSubstring("A=1"))
	})

	// A session opened while the administrator allowed private addresses must not
	// outlive that permission.
	It("closes the open sessions to private addresses when they are turned off", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(missing).To(BeEmpty())
		Expect(tools).NotTo(BeEmpty())
		var echo mcpconns.Tool
		for _, tl := range tools {
			if tl.Name == "echo" {
				echo = tl
			}
		}
		_, err := echo.Run(ctx, json.RawMessage(`{"text":"hi"}`))
		Expect(err).NotTo(HaveOccurred(), "a call while private addresses are allowed")
		before := f.inits.Load()

		e.private.Store(false)
		// A tool resolved earlier is refused at once.
		_, err = echo.Run(ctx, json.RawMessage(`{"text":"again"}`))
		Expect(err).To(HaveOccurred(), "a tool of a session to a private address still ran after the switch was turned off")
		// The next resolve neither reuses the session nor connects.
		tools, missing = e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(tools).To(BeEmpty(), "resolve after the switch must leave the connector missing")
		Expect(missing).To(HaveLen(1), "resolve after the switch must leave the connector missing")
		Expect(e.svc.Sessions()).To(BeZero(), "sessions are still open")
		Expect(f.inits.Load()).To(Equal(before), "the server on a private address was contacted again")
	})

	It("closes the local command sessions of a demoted administrator", func() {
		ctx := specCtx()
		self, err := os.Executable()
		Expect(err).NotTo(HaveOccurred())
		env := map[string]string{"MCPCONNS_HELPER": "1"}
		e := newEnv()
		e.admin.Store(true)
		c, err := e.svc.Create(ctx, "u1", mcpconns.Input{Name: "Local", Transport: mcpconns.TransportStdio, Command: self, Env: &env, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		tools, _ := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(tools).NotTo(BeEmpty(), "the administrator's command gave no tools")
		Expect(e.svc.Sessions()).To(Equal(1))
		e.admin.Store(false)
		e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(e.svc.Sessions()).To(BeZero(), "the demoted user's command is still running")
	})
})
