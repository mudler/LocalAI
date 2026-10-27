package mcpconns_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func newFakeServer(t *testing.T, slow time.Duration) *fakeServer {
	t.Helper()
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
	t.Cleanup(func() { f.CloseClientConnections(); f.Close() })
	return f
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func (f *fakeServer) sawAuth(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.auths {
		if a == want {
			return true
		}
	}
	return false
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

func newEnv(t *testing.T, mutate ...func(*mcpconns.Options)) *env {
	t.Helper()
	e := &env{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	e.permit.Store(true)
	e.private.Store(true) // the test servers listen on loopback
	store, err := mcpconns.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	e.svc = svc
	return e
}

func (e *env) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }

func str(s string) *string { return &s }

func httpInput(name, url string) mcpconns.Input {
	return mcpconns.Input{Name: name, Transport: mcpconns.TransportHTTP, URL: url, Enabled: true}
}

func TestCreateSealsSecretsAndNeverReturnsThem(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	hdr := map[string]string{"X-Team": "blue-team-secret"}
	in := httpInput("Docs", "https://mcp.example/mcp")
	in.Token, in.Headers = str("tok-123456"), &hdr
	c, err := e.svc.Create(ctx, "u1", in)
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasToken || !c.HasHeaders || c.HasEnv || c.Secrets != nil {
		t.Fatalf("the record leaks or misses a flag: %+v", c)
	}
	raw, _ := e.store.Get(ctx, c.ID)
	if bytes.Contains(raw.Secrets, []byte("tok-123456")) || bytes.Contains(raw.Secrets, []byte("blue-team-secret")) || len(raw.Secrets) == 0 {
		t.Fatal("the secrets are not sealed in the store")
	}
	if j, _ := json.Marshal(c); bytes.Contains(j, []byte("tok-123456")) {
		t.Fatal("the token is in the JSON of a connector")
	}
	// A sealed value cannot be moved to another connector or owner.
	other := raw
	other.ID, other.Name = "stolen", "Stolen"
	_ = e.store.Put(ctx, other)
	if p, err := e.svc.Test(ctx, "u1", "stolen"); err != nil || p.Error == "" {
		t.Fatalf("a sealed value that was moved must not open: %+v %v", p, err)
	}
}

func TestUpdateKeepsSecretsUnlessGivenAndCanClearThem(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	in := httpInput("Docs", "https://mcp.example/mcp")
	in.Token = str("tok")
	c, _ := e.svc.Create(ctx, "u1", in)
	up := httpInput("Docs renamed", "https://mcp.example/mcp")
	got, err := e.svc.Update(ctx, "u1", c.ID, up)
	if err != nil || !got.HasToken || got.Name != "Docs renamed" {
		t.Fatalf("an update without a token must keep it: %+v %v", got, err)
	}
	up.Token = str("")
	got, _ = e.svc.Update(ctx, "u1", c.ID, up)
	if got.HasToken {
		t.Fatal("an empty token must clear it")
	}
}

func TestAnotherUserNeverSeesEditsTestsOrUsesAConnector(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, err := e.svc.Create(ctx, "u1", httpInput("Mine", f.URL))
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.List(ctx, "u2"); len(l) != 0 {
		t.Fatalf("u2 lists %+v", l)
	}
	if _, err := e.svc.Get(ctx, "u2", c.ID); !errors.Is(err, mcpconns.ErrNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := e.svc.Update(ctx, "u2", c.ID, httpInput("Hijack", f.URL)); !errors.Is(err, mcpconns.ErrNotFound) {
		t.Fatalf("update: %v", err)
	}
	if _, err := e.svc.SetEnabled(ctx, "u2", c.ID, false); !errors.Is(err, mcpconns.ErrNotFound) {
		t.Fatalf("enable: %v", err)
	}
	if _, err := e.svc.Test(ctx, "u2", c.ID); !errors.Is(err, mcpconns.ErrNotFound) {
		t.Fatalf("test: %v", err)
	}
	if err := e.svc.Delete(ctx, "u2", c.ID); !errors.Is(err, mcpconns.ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
	tools, missing := e.svc.Resolve(ctx, "u2", []string{c.ID}, false)
	if len(tools) != 0 || len(missing) != 1 {
		t.Fatalf("u2 used the connector of u1: %v %v", tools, missing)
	}
	if tools, _ := e.svc.Resolve(ctx, "u2", nil, true); len(tools) != 0 {
		t.Fatal("u2 got the connectors of u1 in all-mode")
	}
	if got, _ := e.svc.Get(ctx, "u1", c.ID); got.Name != "Mine" {
		t.Fatal("the owner's connector was changed")
	}
	if !strings.Contains(strings.Join(missing, " "), "could not be used") && missing[0] == "" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestPermissionIsCheckedOnEveryUse(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Mine", f.URL))
	if tools, _ := e.svc.Resolve(ctx, "u1", []string{c.ID}, false); len(tools) == 0 {
		t.Fatal("no tools with the permission")
	}
	e.permit.Store(false)
	if _, err := e.svc.Create(ctx, "u1", httpInput("Other", f.URL)); !errors.Is(err, mcpconns.ErrForbidden) {
		t.Fatalf("create without the feature: %v", err)
	}
	if _, err := e.svc.Test(ctx, "u1", c.ID); !errors.Is(err, mcpconns.ErrForbidden) {
		t.Fatalf("test without the feature: %v", err)
	}
	tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	if len(tools) != 0 || len(missing) != 1 {
		t.Fatalf("a revoked feature still gave tools: %v %v", tools, missing)
	}
	if tools, _ := e.svc.Resolve(ctx, "u1", nil, true); len(tools) != 0 {
		t.Fatal("all-mode gave tools without the feature")
	}
}

func TestValidationRefusals(t *testing.T) {
	e := newEnv(t)
	e.private.Store(false)
	ctx := t.Context()
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
		if _, err := e.svc.Create(ctx, "u1", in); !errors.Is(err, mcpconns.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A disabled connector with a private address may be saved (an import or a
	// conversion makes them), but cannot be switched on.
	off := httpInput("Internal", "http://10.1.2.3/mcp")
	off.Enabled = false
	c, err := e.svc.Create(ctx, "u1", off)
	if err != nil {
		t.Fatalf("a disabled connector with a private address: %v", err)
	}
	if _, err := e.svc.SetEnabled(ctx, "u1", c.ID, true); !errors.Is(err, mcpconns.ErrInvalid) {
		t.Fatalf("enabling a private address: %v", err)
	}
	e.private.Store(true)
	if _, err := e.svc.SetEnabled(ctx, "u1", c.ID, true); err != nil {
		t.Fatalf("enabling once the administrator allows it: %v", err)
	}
	// Names are unique per user.
	if _, err := e.svc.Create(ctx, "u1", httpInput("Internal", "https://mcp.example")); !errors.Is(err, mcpconns.ErrInvalid) {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := e.svc.Create(ctx, "u2", httpInput("Internal", "https://mcp.example")); err != nil {
		t.Fatalf("the same name for another user: %v", err)
	}
}

func TestAtMostPerUser(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < mcpconns.MaxPerUser; i++ {
		in := httpInput("c"+string(rune('a'+i)), "https://mcp.example")
		if _, err := e.svc.Create(t.Context(), "u1", in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Create(t.Context(), "u1", httpInput("one more", "https://mcp.example")); !errors.Is(err, mcpconns.ErrInvalid) {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestTestConnectionListsToolsAndSendsTheSecretsOnlyToTheServer(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	hdr := map[string]string{"X-Team": "blue"}
	in := httpInput("Docs", f.URL)
	in.Token, in.Headers = str("tok-abcdef"), &hdr
	in.Enabled = false
	c, _ := e.svc.Create(ctx, "u1", in)
	p, err := e.svc.Test(ctx, "u1", c.ID)
	if err != nil || p.Error != "" {
		t.Fatalf("probe: %+v %v", p, err)
	}
	if strings.Join(p.Tools, ",") != "echo,slow,spawn_agent" {
		t.Fatalf("tools = %v", p.Tools)
	}
	if !f.sawAuth("Bearer tok-abcdef") {
		t.Fatal("the server did not get the bearer token")
	}
	f.mu.Lock()
	seen := strings.Join(f.headers, ",")
	f.mu.Unlock()
	if !strings.Contains(seen, "blue") {
		t.Fatal("the server did not get the custom header")
	}
}

func TestAnUnreachableServerGivesAnErrorThatHoldsNoSecret(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	in := httpInput("Dead", url)
	in.Token = str("tok-supersecret")
	c, _ := e.svc.Create(ctx, "u1", in)
	p, err := e.svc.Test(ctx, "u1", c.ID)
	if err != nil || p.Error == "" || len(p.Tools) != 0 {
		t.Fatalf("probe of a dead server: %+v %v", p, err)
	}
	if strings.Contains(p.Error, "tok-supersecret") {
		t.Fatal("the error echoes the token")
	}
	tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	if len(tools) != 0 || len(missing) != 1 || !strings.Contains(missing[0], "Dead") {
		t.Fatalf("an unreachable connector must be a note: %v %v", tools, missing)
	}
}

func TestPrivateAddressesAreRefusedWhenTheConnectionIsMade(t *testing.T) {
	e := newEnv(t)
	e.private.Store(false)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	// The record was saved when private addresses were allowed (or by another
	// route); the name resolves to loopback when the dial happens.
	port := f.URL[strings.LastIndex(f.URL, ":"):]
	for name, url := range map[string]string{"by name": "http://localhost" + port, "by address": f.URL, "ipv6": "http://[::1]" + port} {
		_ = e.store.Put(ctx, mcpconns.Connector{ID: "x-" + name, Owner: "u1", Name: name, Transport: mcpconns.TransportHTTP, URL: url, Enabled: true})
		p, err := e.svc.Test(ctx, "u1", "x-"+name)
		if err != nil || p.Error == "" || len(p.Tools) != 0 {
			t.Errorf("%s: probe = %+v %v", name, p, err)
		}
		if strings.Contains(p.Error, "mcpconns:") {
			t.Errorf("%s: the reason shows the package prefix: %q", name, p.Error)
		}
		if tools, missing := e.svc.Resolve(ctx, "u1", []string{"x-" + name}, false); len(tools) != 0 || len(missing) != 1 {
			t.Errorf("%s: resolve = %v %v", name, tools, missing)
		}
	}
	if f.inits.Load() != 0 {
		t.Fatal("the server on loopback was reached")
	}
	e.private.Store(true)
	if p, _ := e.svc.Test(ctx, "u1", "x-by address"); p.Error != "" {
		t.Fatalf("with the administrator's switch on: %+v", p)
	}
}

func TestARedirectToAnInternalAddressIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	target := newFakeServer(t, 0)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)
	e.private.Store(true)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Redirecting", redirect.URL))
	if p, _ := e.svc.Test(ctx, "u1", c.ID); p.Error == "" {
		t.Fatal("a redirect was followed")
	}
	if target.inits.Load() != 0 {
		t.Fatal("the redirect target was reached")
	}
}

func TestResolveGivesToolsThatRunAndCapsNothingItDoesNotOwn(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
	off := httpInput("Off", f.URL)
	off.Enabled = false
	d, _ := e.svc.Create(ctx, "u1", off)

	tools, missing := e.svc.Resolve(ctx, "u1", nil, true)
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	var echo *mcpconns.Tool
	for i := range tools {
		if tools[i].Name == "echo" {
			echo = &tools[i]
		}
		if tools[i].ConnectorID != c.ID || tools[i].Connector != "Docs" {
			t.Fatalf("tool of the wrong connector: %+v", tools[i])
		}
	}
	if echo == nil {
		t.Fatalf("no echo tool in %+v", tools)
	}
	out, err := echo.Run(ctx, json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "echo: hi" {
		t.Fatalf("run = %q %v", out, err)
	}
	// A disabled connector named by id is a note, never a failure.
	tools, missing = e.svc.Resolve(ctx, "u1", []string{d.ID, "gone"}, false)
	if len(tools) != 0 || len(missing) != 2 {
		t.Fatalf("disabled or deleted: %v %v", tools, missing)
	}
}

func TestACallThatTakesTooLongTimesOut(t *testing.T) {
	e := newEnv(t, func(o *mcpconns.Options) { o.CallTimeout = 150 * time.Millisecond })
	ctx := t.Context()
	f := newFakeServer(t, 5*time.Second)
	_, _ = e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
	tools, _ := e.svc.Resolve(ctx, "u1", nil, true)
	for _, tl := range tools {
		if tl.Name == "slow" {
			start := time.Now()
			if _, err := tl.Run(ctx, json.RawMessage(`{"text":"x"}`)); err == nil || time.Since(start) > 3*time.Second {
				t.Fatalf("slow call: %v after %v", err, time.Since(start))
			}
			return
		}
	}
	t.Fatal("no slow tool")
}

func TestSessionsAreCachedPerConnectorAndClosedOnEditDisableAndDelete(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
	resolve := func() int {
		tools, _ := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		return len(tools)
	}
	if resolve() == 0 || resolve() == 0 {
		t.Fatal("no tools")
	}
	if n := f.inits.Load(); n != 1 {
		t.Fatalf("two resolves opened %d sessions", n)
	}
	if e.svc.Sessions() != 1 {
		t.Fatalf("sessions = %d", e.svc.Sessions())
	}
	if _, err := e.svc.Update(ctx, "u1", c.ID, httpInput("Docs", f.URL)); err != nil {
		t.Fatal(err)
	}
	if e.svc.Sessions() != 0 {
		t.Fatal("an edit left the session open")
	}
	resolve()
	if f.inits.Load() != 2 {
		t.Fatalf("after an edit the next use must reconnect: %d", f.inits.Load())
	}
	_, _ = e.svc.SetEnabled(ctx, "u1", c.ID, false)
	if e.svc.Sessions() != 0 {
		t.Fatal("disabling left the session open")
	}
	_, _ = e.svc.SetEnabled(ctx, "u1", c.ID, true)
	resolve()
	if err := e.svc.Delete(ctx, "u1", c.ID); err != nil {
		t.Fatal(err)
	}
	if e.svc.Sessions() != 0 {
		t.Fatal("a delete left the session open")
	}
}

func TestAnEditOnAnotherFrontendIsNoticedByTheVersion(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
	e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	// Another frontend writes the shared record without telling this one.
	raw, _ := e.store.Get(ctx, c.ID)
	raw.Updated = raw.Updated.Add(time.Minute)
	_ = e.store.Put(ctx, raw)
	e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	if f.inits.Load() != 2 {
		t.Fatalf("a changed record must reconnect: %d", f.inits.Load())
	}
}

func TestIdleSessionsAreClosed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
	e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	e.advance(30 * time.Second)
	e.svc.ReapIdle()
	if e.svc.Sessions() != 1 {
		t.Fatal("a session in use was reaped")
	}
	e.advance(2 * time.Minute)
	e.svc.ReapIdle()
	if e.svc.Sessions() != 0 {
		t.Fatal("an idle session stayed")
	}
}

func TestStdioNeedsAnAdministratorAndIsRefusedInAClusterAndRunsWithoutTheServerEnvironment(t *testing.T) {
	ctx := t.Context()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAI_TEST_SERVER_SECRET", "do-not-leak")
	env := map[string]string{"MCPCONNS_HELPER": "1"}
	in := mcpconns.Input{Name: "Local", Transport: mcpconns.TransportStdio, Command: self, Env: &env, Enabled: true}

	e := newEnv(t)
	if _, err := e.svc.Create(ctx, "u1", in); !errors.Is(err, mcpconns.ErrForbidden) {
		t.Fatalf("a user who is not an administrator: %v", err)
	}
	e.admin.Store(true)
	c, err := e.svc.Create(ctx, "u1", in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.Test(ctx, "u1", c.ID)
	if err != nil || p.Error != "" || strings.Join(p.Tools, ",") != "local_echo" {
		t.Fatalf("stdio probe: %+v %v", p, err)
	}
	// Demoted: the connector stays but cannot be used.
	e.admin.Store(false)
	if tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false); len(tools) != 0 || len(missing) != 1 {
		t.Fatalf("a demoted owner still ran a command: %v %v", tools, missing)
	}

	cluster := newEnv(t, func(o *mcpconns.Options) { o.Distributed = true })
	cluster.admin.Store(true)
	if _, err := cluster.svc.Create(ctx, "u1", in); !errors.Is(err, mcpconns.ErrInvalid) || !strings.Contains(err.Error(), "distributed") {
		t.Fatalf("stdio in a cluster: %v", err)
	}
}

func TestStdioEnvironmentIsMinimalPlusTheSealedOne(t *testing.T) {
	got := mcpconns.StdioEnv(map[string]string{"A": "1"}, []string{"PATH=/bin", "HOME=/h", "LOCALAI_API_KEY=secret", "AWS_SECRET_ACCESS_KEY=x"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "secret") || strings.Contains(joined, "AWS") || !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "A=1") {
		t.Fatalf("env = %v", got)
	}
}

// A session opened while the administrator allowed private addresses must not
// outlive that permission.
func TestTurningOffPrivateAddressesClosesTheOpenSessionsToThem(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	f := newFakeServer(t, 0)
	c, _ := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
	tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	if len(missing) != 0 || len(tools) == 0 {
		t.Fatalf("resolve = %v %v", tools, missing)
	}
	var echo mcpconns.Tool
	for _, tl := range tools {
		if tl.Name == "echo" {
			echo = tl
		}
	}
	if _, err := echo.Run(ctx, json.RawMessage(`{"text":"hi"}`)); err != nil {
		t.Fatalf("a call while private addresses are allowed: %v", err)
	}
	before := f.inits.Load()

	e.private.Store(false)
	// A tool resolved earlier is refused at once.
	if _, err := echo.Run(ctx, json.RawMessage(`{"text":"again"}`)); err == nil {
		t.Fatal("a tool of a session to a private address still ran after the switch was turned off")
	}
	// The next resolve neither reuses the session nor connects.
	if tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false); len(tools) != 0 || len(missing) != 1 {
		t.Fatalf("resolve after the switch = %v %v, want the connector missing", tools, missing)
	}
	if e.svc.Sessions() != 0 {
		t.Fatalf("%d sessions are still open", e.svc.Sessions())
	}
	if f.inits.Load() != before {
		t.Fatal("the server on a private address was contacted again")
	}
}

func TestDemotingTheAdministratorClosesTheirLocalCommandSessions(t *testing.T) {
	ctx := t.Context()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MCPCONNS_HELPER": "1"}
	e := newEnv(t)
	e.admin.Store(true)
	c, err := e.svc.Create(ctx, "u1", mcpconns.Input{Name: "Local", Transport: mcpconns.TransportStdio, Command: self, Env: &env, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if tools, _ := e.svc.Resolve(ctx, "u1", []string{c.ID}, false); len(tools) == 0 {
		t.Fatal("the administrator's command gave no tools")
	}
	if e.svc.Sessions() != 1 {
		t.Fatalf("sessions = %d, want 1", e.svc.Sessions())
	}
	e.admin.Store(false)
	e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
	if e.svc.Sessions() != 0 {
		t.Fatalf("the demoted user's command is still running: %d sessions", e.svc.Sessions())
	}
}
