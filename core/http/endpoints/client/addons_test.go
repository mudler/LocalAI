//go:build auth

package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// fakeHost records what the proxy forwards.
type fakeHost struct {
	mu     sync.Mutex
	routes []contract.ClientRoute
	// byAddon, when it has an entry for an add-on, replaces routes for it.
	byAddon  map[string][]contract.ClientRoute
	addonIDs []string // the add-on of each call
	calls    []contract.ClientRequest
	actors   []contract.Actor
	reply    func(ctx context.Context, r contract.ClientRequest) (contract.ClientResponse, error)
	streamFn func(ctx context.Context, r contract.ClientRequest) (io.ReadCloser, error)
}

func (f *fakeHost) ClientRoutes(_ context.Context, id string) ([]contract.ClientRoute, error) {
	if rs, ok := f.byAddon[id]; ok {
		return rs, nil
	}
	return f.routes, nil
}
func (f *fakeHost) ClientCall(ctx context.Context, id string, a contract.Actor, r contract.ClientRequest) (contract.ClientResponse, error) {
	f.mu.Lock()
	f.addonIDs = append(f.addonIDs, id)
	f.calls = append(f.calls, r)
	f.actors = append(f.actors, a)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(ctx, r)
	}
	return contract.ClientResponse{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, nil
}
func (f *fakeHost) ClientStream(ctx context.Context, _ string, _ contract.Actor, r contract.ClientRequest) (io.ReadCloser, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r)
	f.mu.Unlock()
	return f.streamFn(ctx, r)
}
func (f *fakeHost) called() []contract.ClientRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contract.ClientRequest(nil), f.calls...)
}

// closeRecorder is an upstream stream the test feeds and watches.
type closeRecorder struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	closed chan struct{}
	once   sync.Once
}

func newUpstream() *closeRecorder {
	pr, pw := io.Pipe()
	return &closeRecorder{pr: pr, pw: pw, closed: make(chan struct{})}
}
func (c *closeRecorder) Read(p []byte) (int, error) { return c.pr.Read(p) }
func (c *closeRecorder) Close() error {
	c.once.Do(func() { close(c.closed); _ = c.pw.Close(); _ = c.pr.Close() })
	return nil
}

var _ = Describe("client add-on proxy", func() {
	var (
		db     *gorm.DB
		host   *fakeHost
		proxy  *Proxy
		srv    *httptest.Server
		key    string
		keyID  string
		admin  string
		secret = ""
	)

	routes := []contract.ClientRoute{
		{ID: "ping", Method: "GET", Path: "/ping"},
		{ID: "start", Method: "POST", Path: "/jobs", MaxBodyBytes: 64},
		{ID: "job", Method: "GET", Path: "/jobs/{id}"},
		{ID: "slow", Method: "GET", Path: "/slow", TimeoutSeconds: 1},
		{ID: "events", Method: "GET", Path: "/events", Stream: true},
	}

	var pairClientFor func(addons []string, features ...string) (string, string)
	pairClient := func(features ...string) (string, string) {
		owner := &auth.User{ID: "owner", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		if err := db.FirstOrCreate(owner, "id = ?", "owner").Error; err != nil {
			Fail(err.Error())
		}
		return pairClientFor(nil, features...)
	}
	pairClientFor = func(addons []string, features ...string) (string, string) {
		if addons == nil && slices.Contains(features, auth.FeatureClientAddons) {
			addons = []string{"demo", "notes-sync", "bare", "other"}
		}
		code, _, err := auth.CreatePairingCode(db, "owner", "front door", features, addons, 0, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		plain, k, err := auth.RedeemPairingCode(db, code, "", secret)
		Expect(err).ToNot(HaveOccurred())
		return plain, k.ID
	}

	do := func(method, target, token string, body io.Reader, hdr ...string) (*http.Response, []byte) {
		req, err := http.NewRequest(method, srv.URL+target, body)
		Expect(err).ToNot(HaveOccurred())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		host = &fakeHost{routes: routes}
		proxy = NewProxy(func() Host { return host }, func(owner, id string) bool { return auth.ClientKeyActive(db, id, owner) })
		proxy.heartbeat = 50 * time.Millisecond

		e := echo.New()
		e.Use(auth.Middleware(db, &config.ApplicationConfig{}))
		e.Use(auth.RequireRouteFeature(db))
		for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			e.Add(m, "/api/client/addons/:addon/*", proxy.Handler())
		}
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)

		key, keyID = pairClient(auth.FeatureClientSelf, auth.FeatureClientAddons)
		owner := &auth.User{}
		Expect(db.First(owner, "id = ?", "owner").Error).To(Succeed())
		admin, err = auth.CreateSession(db, "owner", secret)
		Expect(err).ToNot(HaveOccurred())
	})

	It("limits a key to the add-ons of its list, with one answer for all the rest", func() {
		host.byAddon = map[string][]contract.ClientRoute{"notes-sync": {{ID: "list", Method: "GET", Path: "/notes"}}}
		onlyDemo, _ := pairClientFor([]string{"demo"}, auth.FeatureClientSelf, auth.FeatureClientAddons)
		resp, _ := do("GET", "/api/client/addons/demo/ping", onlyDemo, nil)
		Expect(resp.StatusCode).To(Equal(200))
		before := len(host.called())
		// Another add-on that exists, one that does not, and a malformed id all
		// get the same 403, and the add-on never hears of the call.
		var bodies []string
		for _, id := range []string{"notes-sync", "no-such-addon", "Bad_Id"} {
			resp, b := do("GET", "/api/client/addons/"+id+"/notes", onlyDemo, nil)
			Expect(resp.StatusCode).To(Equal(403), id)
			bodies = append(bodies, string(b))
		}
		Expect(bodies[1]).To(Equal(bodies[0]))
		Expect(bodies[2]).To(Equal(bodies[0]))
		Expect(host.called()).To(HaveLen(before))
		// The key of the other add-on is the mirror image.
		onlyNotes, _ := pairClientFor([]string{"notes-sync"}, auth.FeatureClientAddons)
		resp, _ = do("GET", "/api/client/addons/notes-sync/notes", onlyNotes, nil)
		Expect(resp.StatusCode).To(Equal(200))
		resp, _ = do("GET", "/api/client/addons/demo/ping", onlyNotes, nil)
		Expect(resp.StatusCode).To(Equal(403))
	})

	It("reaches nothing with client_addons and an empty list, also after the list was emptied", func() {
		k, id := pairClientFor([]string{"demo"}, auth.FeatureClientAddons)
		resp, _ := do("GET", "/api/client/addons/demo/ping", k, nil)
		Expect(resp.StatusCode).To(Equal(200))
		_, err := auth.SetClientKeyAddons(db, id, "owner", []string{})
		Expect(err).ToNot(HaveOccurred())
		resp, _ = do("GET", "/api/client/addons/demo/ping", k, nil)
		Expect(resp.StatusCode).To(Equal(403))
	})

	It("serves any add-on the same way: other id, other routes, no jobs and no events", func() {
		host.byAddon = map[string][]contract.ClientRoute{
			"notes-sync": {
				{ID: "list", Method: "GET", Path: "/notes"},
				{ID: "put", Method: "PUT", Path: "/notes/{name}", MaxBodyBytes: 128},
			},
			"bare": nil, // an add-on that declared no client route
		}
		resp, body := do("GET", "/api/client/addons/notes-sync/notes", key, nil)
		Expect(resp.StatusCode).To(Equal(200), string(body))
		resp, _ = do("PUT", "/api/client/addons/notes-sync/notes/todo", key, strings.NewReader(`{"text":"x"}`))
		Expect(resp.StatusCode).To(Equal(200))
		calls := host.called()
		Expect(calls).To(HaveLen(2))
		Expect(calls[1].RouteID).To(Equal("put"))
		Expect(calls[1].Params).To(Equal(map[string]string{"name": "todo"}))
		Expect(host.addonIDs).To(Equal([]string{"notes-sync", "notes-sync"}))
		// The routes of another add-on do not exist here, and an add-on with
		// none serves nothing.
		for _, p := range []string{"/api/client/addons/notes-sync/ping", "/api/client/addons/notes-sync/jobs", "/api/client/addons/notes-sync/events", "/api/client/addons/bare/notes"} {
			resp, _ = do("GET", p, key, nil)
			Expect(resp.StatusCode).To(Equal(404), p)
		}
		// The default routes of the other tests still serve their own add-on.
		resp, _ = do("GET", "/api/client/addons/demo/ping", key, nil)
		Expect(resp.StatusCode).To(Equal(200))
	})

	It("forwards a declared route with the identity of the client and never its credential", func() {
		resp, body := do("GET", "/api/client/addons/demo/jobs/j-1?a=b", key, nil)
		Expect(resp.StatusCode).To(Equal(200))
		Expect(string(body)).To(Equal(`{"ok":true}`))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
		Expect(resp.Header.Get("Cache-Control")).To(Equal("no-store"))
		calls := host.called()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].RouteID).To(Equal("job"))
		Expect(calls[0].Params).To(Equal(map[string]string{"id": "j-1"}))
		Expect(calls[0].Query).To(Equal(map[string]string{"a": "b"}))
		Expect(calls[0].Client).To(Equal(contract.ClientIdentity{ID: keyID, Name: "front door"}))
		// The host records the call under the owner and the client key.
		Expect(host.actors).To(ConsistOf(contract.Actor{UserID: "owner", CredentialID: "client:" + keyID, Mode: "named-user"}))
		raw, _ := json.Marshal(calls[0])
		Expect(string(raw)).ToNot(ContainSubstring(key))
		Expect(string(raw)).ToNot(ContainSubstring("owner"))
	})

	It("forwards a wildcard tail as one slash-joined parameter, and lets a literal route win", func() {
		host.byAddon = map[string][]contract.ClientRoute{"notes-sync": {
			{ID: "file", Method: "GET", Path: "/files/{path...}"},
			{ID: "index", Method: "GET", Path: "/files/index"},
		}}
		resp, _ := do("GET", "/api/client/addons/notes-sync/files/2026/11/trip.md", key, nil)
		Expect(resp.StatusCode).To(Equal(200))
		resp, _ = do("GET", "/api/client/addons/notes-sync/files/index", key, nil)
		Expect(resp.StatusCode).To(Equal(200))
		calls := host.called()
		Expect(calls).To(HaveLen(2))
		Expect(calls[0].RouteID).To(Equal("file"))
		Expect(calls[0].Params).To(Equal(map[string]string{"path": "2026/11/trip.md"}))
		Expect(calls[0].Path).To(Equal("/files/2026/11/trip.md"))
		Expect(calls[1].RouteID).To(Equal("index"))
		// A tail never carries an escape, a dot segment or an empty segment.
		for target, want := range map[string]int{
			"/api/client/addons/notes-sync/files":                                                             404,
			"/api/client/addons/notes-sync/files/a//b":                                                        404,
			"/api/client/addons/notes-sync/files/a/b/":                                                        404,
			"/api/client/addons/notes-sync/files/a/%2e%2e/b":                                                  400,
			"/api/client/addons/notes-sync/files/a%2fb":                                                       400,
			"/api/client/addons/notes-sync/files/a%5cb":                                                       400,
			"/api/client/addons/notes-sync/files/" + strings.Repeat("a/", contract.MaxClientTailPathSegments): 404,
		} {
			resp, _ := do("GET", target, key, nil)
			Expect(resp.StatusCode).To(Equal(want), target)
		}
		Expect(host.called()).To(HaveLen(2), "no refused request reached the add-on")
	})

	It("passes a JSON body within the limit", func() {
		resp, _ := do("POST", "/api/client/addons/demo/jobs", key, strings.NewReader(`{"x":1}`))
		Expect(resp.StatusCode).To(Equal(200))
		Expect(host.called()[0].Body).To(Equal([]byte(`{"x":1}`)))
		Expect(host.called()[0].ContentType).To(Equal("application/json"))
	})

	It("refuses what is not declared without calling the add-on", func() {
		for target, want := range map[string]int{
			"/api/client/addons/demo/nope":           404,
			"/api/client/addons/demo/jobs/a/b":       404,
			"/api/client/addons/demo/ping/":          404,
			"/api/client/addons/demo/jobs/%2e%2e":    400,
			"/api/client/addons/demo/jobs/a%2fb":     400,
			"/api/client/addons/demo/jobs/..%2fping": 400,
			"/api/client/addons/demo/jobs/a%5cb":     400,
			"/api/client/addons/Demo/ping":           403,
			"/api/client/addons/not_valid/ping":      403,
		} {
			resp, _ := do("GET", target, key, nil)
			Expect(resp.StatusCode).To(Equal(want), target)
		}
		// Go cleans a dot segment before routing; it must not reach another route.
		resp, _ := do("GET", "/api/client/addons/demo/jobs/../ping", key, nil)
		Expect(resp.StatusCode).To(BeNumerically(">=", 300))
		Expect(host.called()).To(BeEmpty())
		resp, _ = do("DELETE", "/api/client/addons/demo/ping", key, nil)
		Expect(resp.StatusCode).To(Equal(405))
		Expect(host.called()).To(BeEmpty())
	})

	It("refuses a request without a client credential", func() {
		resp, _ := do("GET", "/api/client/addons/demo/ping", "", nil)
		Expect(resp.StatusCode).To(Equal(401))
		// An administrator passes the feature check but is not a client.
		resp, _ = do("GET", "/api/client/addons/demo/ping", admin, nil)
		Expect(resp.StatusCode).To(Equal(403))
		// A client key without the feature is refused by the gate.
		narrow, _ := pairClient(auth.FeatureClientSelf)
		resp, _ = do("GET", "/api/client/addons/demo/ping", narrow, nil)
		Expect(resp.StatusCode).To(Equal(403))
		Expect(host.called()).To(BeEmpty())
	})

	It("limits the body, its type and a body on a route that takes none", func() {
		resp, _ := do("POST", "/api/client/addons/demo/jobs", key, strings.NewReader(strings.Repeat("a", 65)))
		Expect(resp.StatusCode).To(Equal(413))
		// Unknown length: the reader stops at the limit all the same.
		resp, _ = do("POST", "/api/client/addons/demo/jobs", key, io.NopCloser(strings.NewReader(strings.Repeat("a", 5000))))
		Expect(resp.StatusCode).To(Equal(413))
		resp, _ = do("POST", "/api/client/addons/demo/jobs", key, strings.NewReader(`{}`), "Content-Type", "text/plain")
		Expect(resp.StatusCode).To(Equal(415))
		resp, _ = do("GET", "/api/client/addons/demo/ping", key, strings.NewReader(`{}`))
		Expect(resp.StatusCode).To(Equal(400))
		Expect(host.called()).To(BeEmpty())
	})

	It("limits the query", func() {
		var q []string
		for i := range 17 {
			q = append(q, "k"+string(rune('a'+i))+"=v")
		}
		resp, _ := do("GET", "/api/client/addons/demo/ping?"+strings.Join(q, "&"), key, nil)
		Expect(resp.StatusCode).To(Equal(400))
	})

	It("times out a call that does not answer", func() {
		host.reply = func(ctx context.Context, _ contract.ClientRequest) (contract.ClientResponse, error) {
			<-ctx.Done()
			return contract.ClientResponse{}, ctx.Err()
		}
		start := time.Now()
		resp, _ := do("GET", "/api/client/addons/demo/slow", key, nil)
		Expect(resp.StatusCode).To(Equal(504))
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
	})

	It("does not pass a header or a status the add-on should not set", func() {
		host.reply = func(context.Context, contract.ClientRequest) (contract.ClientResponse, error) {
			return contract.ClientResponse{Status: 302, ContentType: "application/json", Body: []byte("{}")}, nil
		}
		resp, _ := do("GET", "/api/client/addons/demo/ping", key, nil)
		Expect(resp.StatusCode).To(Equal(502))
		Expect(resp.Header.Get("Location")).To(BeEmpty())
	})

	It("limits the calls of one client at once", func() {
		release := make(chan struct{})
		host.reply = func(ctx context.Context, _ contract.ClientRequest) (contract.ClientResponse, error) {
			<-release
			return contract.ClientResponse{Status: 204}, nil
		}
		var wg sync.WaitGroup
		for range MaxCallsPerClient {
			wg.Add(1)
			go func() { defer wg.Done(); defer GinkgoRecover(); do("GET", "/api/client/addons/demo/ping", key, nil) }()
		}
		Eventually(func() int { return len(host.called()) }, 2*time.Second).Should(Equal(MaxCallsPerClient))
		resp, _ := do("GET", "/api/client/addons/demo/ping", key, nil)
		Expect(resp.StatusCode).To(Equal(429))
		close(release)
		wg.Wait()
		// Another client is not affected.
		other, _ := pairClient(auth.FeatureClientAddons)
		host.reply = nil
		resp, _ = do("GET", "/api/client/addons/demo/ping", other, nil)
		Expect(resp.StatusCode).To(Equal(200))
	})

	Describe("streams", func() {
		open := func(hdr ...string) (*http.Response, *bufio.Reader, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/client/addons/demo/events", nil)
			req.Header.Set("Authorization", "Bearer "+key)
			for i := 0; i+1 < len(hdr); i += 2 {
				req.Header.Set(hdr[i], hdr[i+1])
			}
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			return resp, bufio.NewReader(resp.Body), cancel
		}

		It("sends headers at once, flushes each event, beats while quiet and ends when the client leaves", func() {
			up := newUpstream()
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) { return up, nil }
			resp, rd, cancel := open("Last-Event-ID", "e-41")
			defer cancel()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(Equal("text/event-stream"))
			Expect(resp.Header.Get("Content-Encoding")).To(BeEmpty())
			Expect(host.called()[0].LastEventID).To(Equal("e-41"))

			go func() { _, _ = up.pw.Write([]byte("id: e-42\nevent: job.done\ndata: {}\n\n")) }()
			line, err := rd.ReadString('\n')
			Expect(err).ToNot(HaveOccurred())
			Expect(line).To(Equal("id: e-42\n"))
			for range 3 { // the rest of the event
				_, err = rd.ReadString('\n')
				Expect(err).ToNot(HaveOccurred())
			}
			// Quiet: a comment arrives without anything from the add-on.
			hb, err := rd.ReadString('\n')
			Expect(err).ToNot(HaveOccurred())
			Expect(hb).To(Equal(": hb\n"))

			cancel()
			Eventually(up.closed, 2*time.Second).Should(BeClosed())
		})

		It("never writes a heartbeat into the middle of an event", func() {
			up := newUpstream()
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) { return up, nil }
			_, rd, cancel := open()
			defer cancel()
			_, _ = up.pw.Write([]byte("id: 1\ndata: par"))
			// Let several heartbeats fall in the middle of the event.
			start := proxy.ticks.Load()
			Eventually(func() int64 { return proxy.ticks.Load() - start }, 5*time.Second).Should(BeNumerically(">=", 3))
			_, _ = up.pw.Write([]byte("tial\n\n"))
			var got bytes.Buffer
			for !strings.HasSuffix(got.String(), "\n\n") {
				line, err := rd.ReadString('\n')
				Expect(err).ToNot(HaveOccurred())
				got.WriteString(line)
			}
			Expect(got.String()).To(Equal("id: 1\ndata: partial\n\n"))
		})

		It("ends the stream of a key that is no longer paired", func() {
			up := newUpstream()
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) { return up, nil }
			resp, rd, cancel := open()
			defer cancel()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(auth.RevokeClientKey(db, keyID, "owner")).To(Succeed())
			_, err := io.ReadAll(rd)
			Expect(err).ToNot(HaveOccurred(), "the stream ends cleanly")
			Eventually(up.closed, 2*time.Second).Should(BeClosed())
		})

		It("ends the stream when the owner is disabled", func() {
			up := newUpstream()
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) { return up, nil }
			_, _, cancel := open()
			defer cancel()
			Expect(db.Model(&auth.User{}).Where("id = ?", "owner").Update("status", "pending").Error).To(Succeed())
			Eventually(up.closed, 2*time.Second).Should(BeClosed())
		})

		It("ends the stream of a revoked key while the add-on keeps sending", func() {
			up := newUpstream()
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) { return up, nil }
			go func() {
				for {
					if _, err := up.pw.Write([]byte(": busy\n\n")); err != nil {
						return
					}
					time.Sleep(5 * time.Millisecond) // far below the heartbeat
				}
			}()
			resp, rd, cancel := open()
			defer cancel()
			Expect(resp.StatusCode).To(Equal(200))
			_, err := rd.ReadString('\n')
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.RevokeClientKey(db, keyID, "owner")).To(Succeed())
			Eventually(up.closed, 2*time.Second).Should(BeClosed())
		})

		It("ends the response when the add-on ends the stream", func() {
			up := newUpstream()
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) { return up, nil }
			resp, _, cancel := open()
			defer cancel()
			_ = up.pw.Close()
			b, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			_ = b
		})

		It("limits the streams of one client and refuses Last-Event-ID off a stream route", func() {
			var ups []*closeRecorder
			host.streamFn = func(context.Context, contract.ClientRequest) (io.ReadCloser, error) {
				u := newUpstream()
				ups = append(ups, u)
				return u, nil
			}
			for range MaxStreamsPerClient {
				_, _, cancel := open()
				defer cancel()
			}
			resp, _, cancel := open()
			defer cancel()
			Expect(resp.StatusCode).To(Equal(429))
			r, _ := do("GET", "/api/client/addons/demo/ping", key, nil, "Last-Event-ID", "1")
			Expect(r.StatusCode).To(Equal(400))
			r, _ = do("GET", "/api/client/addons/demo/events", key, nil, "Last-Event-ID", strings.Repeat("a", 129))
			Expect(r.StatusCode).To(Equal(400))
		})
	})
})
