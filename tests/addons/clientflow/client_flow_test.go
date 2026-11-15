//go:build linux && auth

// SPDX-License-Identifier: MIT

// Package clientflow runs the whole path of a paired client: the owner
// makes a pairing code, the client pairs, calls a route of a real add-on
// process (the reference add-on) through the HTTP stack with the real
// authentication, starts a job through the host operations, and reads the
// server-sent events, also after the connection dropped.
//
// What is real: the authentication database and middleware, the route
// registry, the client proxy, the runtime supervisor and client, the add-on
// executable, the host operation broker and its loopback channel. What is not:
// the job service, which a small in-memory host replaces (it applies the same
// rule as the real one, that a client sees only its own jobs; the real one has
// its own tests in core/services/studio).
package clientflow_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

func TestClientFlow(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Client end to end")
}

const addonID = "client-example"

var binary string

var _ = BeforeSuite(func() {
	binary = filepath.Join(GinkgoT().TempDir(), addonID)
	cmd := exec.Command("go", "build", "-o", binary, "./addons/internal/clientdemo/cmd/client-example")
	cmd.Dir = "../../.."
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
})

// jobHost is the job service of the test. A client sees only its own jobs.
type jobHost struct {
	db *gorm.DB
	mu sync.Mutex
	n  int
	js map[string]*memJob
}

type memJob struct {
	owner, client, task, status, result string
}

func (h *jobHost) active(c hostops.ClientCall) error {
	if !auth.ClientKeyActive(h.db, c.ClientID, c.OwnerID) {
		return fmt.Errorf("%w: the client is not paired", hostops.ErrForbidden)
	}
	return nil
}

func (h *jobHost) view(id string, j *memJob) hostops.ClientJob {
	return hostops.ClientJob{ID: id, Task: j.task, Status: j.status, Result: j.result}
}

func (h *jobHost) Start(_ context.Context, c hostops.ClientCall, task string, _ map[string]string) (hostops.ClientJob, error) {
	if err := h.active(c); err != nil {
		return hostops.ClientJob{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	id := fmt.Sprintf("job-%d", h.n)
	j := &memJob{owner: c.OwnerID, client: c.ClientID, task: task, status: "pending"}
	h.js[id] = j
	return h.view(id, j), nil
}

func (h *jobHost) find(c hostops.ClientCall, id string) (*memJob, error) {
	j := h.js[id]
	if j == nil || j.owner != c.OwnerID || j.client != c.ClientID {
		return nil, fmt.Errorf("%w: job", hostops.ErrNotFound)
	}
	return j, nil
}

func (h *jobHost) Get(_ context.Context, c hostops.ClientCall, id string) (hostops.ClientJob, error) {
	if err := h.active(c); err != nil {
		return hostops.ClientJob{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	j, err := h.find(c, id)
	if err != nil {
		return hostops.ClientJob{}, err
	}
	return h.view(id, j), nil
}

func (h *jobHost) Cancel(_ context.Context, c hostops.ClientCall, id string) (hostops.ClientJob, error) {
	if err := h.active(c); err != nil {
		return hostops.ClientJob{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	j, err := h.find(c, id)
	if err != nil {
		return hostops.ClientJob{}, err
	}
	j.status = "cancelled"
	return h.view(id, j), nil
}

func (h *jobHost) finish(id, result string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.js[id].status, h.js[id].result = "completed", result
}

// addonHost serves the proxy from one running add-on, as the manager does.
type addonHost struct{ client contract.Runtime }

func (a addonHost) ClientRoutes(_ context.Context, id string) ([]contract.ClientRoute, error) {
	if id != addonID {
		return nil, addonruntime.ErrRuntime
	}
	return a.client.(contract.ClientRouteSource).ClientRoutes(), nil
}

func (a addonHost) ClientCall(ctx context.Context, _ string, actor contract.Actor, r contract.ClientRequest) (contract.ClientResponse, error) {
	return a.client.(contract.ClientCaller).ClientCallAs(ctx, actor, r)
}

func (a addonHost) ClientStream(ctx context.Context, _ string, actor contract.Actor, r contract.ClientRequest) (io.ReadCloser, error) {
	return a.client.(contract.ClientCaller).ClientStreamAs(ctx, actor, r)
}

type event struct{ id, typ, data string }

// reader reads server-sent events from one response.
type reader struct {
	resp *http.Response
	ev   chan event
}

func newReader(resp *http.Response) *reader {
	r := &reader{resp: resp, ev: make(chan event, 64)}
	go func() {
		defer close(r.ev)
		sc := bufio.NewScanner(resp.Body)
		var cur event
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "id: "):
				cur.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				cur.typ = line[7:]
			case strings.HasPrefix(line, "data: "):
				cur.data = line[6:]
			case line == "":
				if cur.typ != "" {
					r.ev <- cur
				}
				cur = event{}
			}
		}
	}()
	return r
}

func (r *reader) next(d time.Duration) (event, bool) {
	select {
	case e, ok := <-r.ev:
		return e, ok
	case <-time.After(d):
		return event{}, false
	}
}

var _ = Describe("a paired client and a real add-on", func() {
	var (
		db       *gorm.DB
		jobs     *jobHost
		srv      *httptest.Server
		admin    string
		secret   = "e2e-secret"
		sup      *addonruntime.Supervisor
		inst     addonruntime.Instance
		bodyJSON = func(v any) io.Reader { b, _ := json.Marshal(v); return bytes.NewReader(b) }
	)

	do := func(method, path, token string, body any, hdr ...string) (int, []byte, http.Header) {
		var rd io.Reader
		if body != nil {
			rd = bodyJSON(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rd)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header
	}

	stream := func(token, last string) (*reader, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/client/addons/"+addonID+"/events", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/event-stream"))
		DeferCleanup(cancel)
		return newReader(resp), cancel
	}

	pairFor := func(addons []string, features ...string) (key, id string) {
		code, b, _ := do(http.MethodPost, "/api/client/pairing-codes", admin, map[string]any{"name": "box", "features": features, "addons": addons})
		Expect(code).To(Equal(http.StatusCreated), string(b))
		var made struct{ Code string }
		Expect(json.Unmarshal(b, &made)).To(Succeed())
		code, b, _ = do(http.MethodPost, "/api/client/pair", "", map[string]string{"code": made.Code})
		Expect(code).To(Equal(http.StatusCreated), string(b))
		var got struct{ Key, ID string }
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		return got.Key, got.ID
	}
	// pair pairs a client that may reach the example add-on when it has the
	// add-on feature.
	pair := func(features ...string) (key, id string) {
		var addons []string
		for _, f := range features {
			if f == auth.FeatureClientAddons {
				addons = []string{addonID}
			}
		}
		return pairFor(addons, features...)
	}
	both := []string{auth.FeatureClientSelf, auth.FeatureClientAddons}

	startJob := func(key string) string {
		code, b, _ := do(http.MethodPost, "/api/client/addons/"+addonID+"/jobs", key, map[string]any{"task": "ask", "params": map[string]string{"q": "hello"}})
		Expect(code).To(Equal(http.StatusAccepted), string(b))
		var j struct{ ID, Status string }
		Expect(json.Unmarshal(b, &j)).To(Succeed())
		Expect(j.ID).NotTo(BeEmpty())
		return j.ID
	}

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Create(&auth.User{ID: "owner", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
		admin, err = auth.CreateSession(db, "owner", secret)
		Expect(err).NotTo(HaveOccurred())

		// The reference add-on, started by the real supervisor, with a host
		// operation channel that serves the client job operations.
		jobs = &jobHost{db: db, js: map[string]*memJob{}}
		limits := contract.DefaultLimits()
		limits.ReadTimeout = 5 * time.Second
		in := hostops.NewInFlight()
		broker := hostops.New(in, func(_, feature string) bool { return feature == "mcp_jobs" }, limits)
		Expect(hostops.RegisterClientJobs(broker, jobs)).To(Succeed())
		sup, err = addonruntime.NewSupervisor(addonruntime.Config{
			StateRoot: GinkgoT().TempDir(), DataRoot: GinkgoT().TempDir(), Limits: limits, ReadyTimeout: 5 * time.Second, StableHealth: time.Second,
			HostOps: &addonruntime.HostOps{Broker: broker, InFlight: in, Authority: hostops.NewStaticAuthority(map[string][]string{addonID: {"client."}})},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sup.Close(context.Background())).To(Succeed()) })

		raw, err := os.ReadFile("../../../addons/internal/clientdemo/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		m.OS, m.Arch = goruntime.GOOS, goruntime.GOARCH
		dir := GinkgoT().TempDir()
		exe, err := os.ReadFile(binary)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, addonID), exe, 0o500)).To(Succeed())
		receipt := packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:example"}
		rb, _ := json.Marshal(receipt)
		Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), rb, 0o400)).To(Succeed())
		inst, err = sup.Start(context.Background(), receipt, 1)
		Expect(err).NotTo(HaveOccurred())

		e := echo.New()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.APIKeyHMACSecret = secret
		e.Use(auth.Middleware(db, cfg))
		e.Use(auth.RequireRouteFeature(db))
		routesCtx, stopRoutes := context.WithCancel(context.Background())
		DeferCleanup(stopRoutes)
		routes.RegisterClientRoutesFor(routesCtx, e, db, secret, auth.RequireAdmin(), addonHost{client: inst.Client}, nil)
		okh := func(c echo.Context) error { return c.String(http.StatusOK, "ok") }
		e.GET("/api/client/unregistered", okh)
		e.GET("/v1/models", okh)
		e.GET("/api/auth/api-keys", okh)
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	It("pairs, starts a job through the add-on, gets job.done, drops and resumes without losing it", func() {
		key, id := pair(both...)
		code, b, _ := do(http.MethodGet, "/api/client/self", key, nil)
		Expect(code).To(Equal(http.StatusOK), string(b))
		Expect(string(b)).To(ContainSubstring(id))

		jobID := startJob(key)

		// The first connection sees the progress event of the start.
		r1, drop := stream(key, "")
		first, ok := r1.next(5 * time.Second)
		Expect(ok).To(BeTrue())
		Expect(first.typ).To(Equal("job.progress"))
		Expect(first.data).To(ContainSubstring(jobID))
		drop() // the connection drops

		// The job ends while the client is away.
		jobs.finish(jobID, "hello world")

		r2, drop2 := stream(key, first.id)
		done, ok := r2.next(5 * time.Second)
		Expect(ok).To(BeTrue(), "no event after the resume")
		Expect(done.typ).To(Equal("job.done"))
		Expect(done.data).To(ContainSubstring("hello world"))
		Expect(done.id).NotTo(Equal(first.id))
		drop2() // dropped before the client could act on it

		// The client resumes from the same id and gets the same event again.
		r3, _ := stream(key, first.id)
		again, ok := r3.next(5 * time.Second)
		Expect(ok).To(BeTrue())
		Expect(again).To(Equal(done))

		// From the last id there is nothing new.
		r4, _ := stream(key, done.id)
		_, ok = r4.next(1500 * time.Millisecond)
		Expect(ok).To(BeFalse(), "an event that the client already has came again")

		// The job state is also readable by id.
		code, b, _ = do(http.MethodGet, "/api/client/addons/"+addonID+"/jobs/"+jobID, key, nil)
		Expect(code).To(Equal(http.StatusOK), string(b))
		Expect(string(b)).To(ContainSubstring("completed"))
	})

	It("sends job.done on an open connection", func() {
		key, _ := pair(both...)
		r, _ := stream(key, "")
		jobID := startJob(key)
		ev, ok := r.next(5 * time.Second)
		Expect(ok).To(BeTrue())
		Expect(ev.typ).To(Equal("job.progress"))
		jobs.finish(jobID, "ok")
		ev, ok = r.next(5 * time.Second)
		Expect(ok).To(BeTrue())
		Expect(ev.typ).To(Equal("job.done"))
	})

	It("keeps a client to its own jobs and its own events", func() {
		k1, _ := pair(both...)
		k2, _ := pair(both...)
		job1 := startJob(k1)
		code, _, _ := do(http.MethodGet, "/api/client/addons/"+addonID+"/jobs/"+job1, k2, nil)
		Expect(code).To(Equal(http.StatusNotFound))
		code, _, _ = do(http.MethodDelete, "/api/client/addons/"+addonID+"/jobs/"+job1, k2, nil)
		Expect(code).To(Equal(http.StatusNotFound))

		startJob(k2)
		r2, _ := stream(k2, "")
		ev, ok := r2.next(5 * time.Second)
		Expect(ok).To(BeTrue())
		Expect(ev.data).NotTo(ContainSubstring(job1), "the stream of one client carried the job of another")
		// Another client cannot resume from the ids of the first either: the
		// outbox is per client, so it simply has its own sequence.
		code, _, _ = do(http.MethodGet, "/api/client/addons/"+addonID+"/jobs/"+job1, k1, nil)
		Expect(code).To(Equal(http.StatusOK))
	})

	It("refuses a revoked key at once", func() {
		key, id := pair(both...)
		Expect(startJob(key)).NotTo(BeEmpty())
		code, _, _ := do(http.MethodDelete, "/api/client/keys/"+id, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		for _, p := range []string{"/api/client/self", "/api/client/addons/" + addonID + "/ping", "/api/client/addons/" + addonID + "/events"} {
			code, _, _ = do(http.MethodGet, p, key, nil)
			Expect(code).To(Equal(http.StatusUnauthorized), p)
		}
	})

	It("refuses a pairing code that was used or has expired, with the same answer", func() {
		code, b, _ := do(http.MethodPost, "/api/client/pairing-codes", admin, map[string]any{})
		Expect(code).To(Equal(http.StatusCreated))
		var made struct{ Code string }
		Expect(json.Unmarshal(b, &made)).To(Succeed())
		c1, _, _ := do(http.MethodPost, "/api/client/pair", "", map[string]string{"code": made.Code})
		c2, b2, _ := do(http.MethodPost, "/api/client/pair", "", map[string]string{"code": made.Code})
		Expect(c1).To(Equal(http.StatusCreated))
		Expect(c2).To(Equal(http.StatusUnauthorized))

		_, b, _ = do(http.MethodPost, "/api/client/pairing-codes", admin, map[string]any{})
		Expect(json.Unmarshal(b, &made)).To(Succeed())
		Expect(db.Model(&auth.ClientPairingCode{}).Where("used_at IS NULL").Update("expires_at", time.Now().Add(-time.Minute)).Error).To(Succeed())
		c3, b3, _ := do(http.MethodPost, "/api/client/pair", "", map[string]string{"code": made.Code})
		Expect(c3).To(Equal(http.StatusUnauthorized))
		Expect(b3).To(Equal(b2))
	})

	It("denies a route that is not in the registry and every route outside the allow-list", func() {
		key, _ := pair(both...)
		for _, p := range []string{"/api/client/unregistered", "/v1/models", "/api/auth/api-keys", "/api/client/keys", "/api/client/pairing-codes", "/nope"} {
			code, _, _ := do(http.MethodGet, p, key, nil)
			Expect(code).To(Equal(http.StatusForbidden), p)
		}
		// A key without client_addons cannot reach the add-on at all.
		narrow, _ := pair(auth.FeatureClientSelf)
		code, _, _ := do(http.MethodGet, "/api/client/addons/"+addonID+"/ping", narrow, nil)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("forwards only the routes the add-on declared", func() {
		key, _ := pair(both...)
		base := "/api/client/addons/" + addonID
		code, b, _ := do(http.MethodGet, base+"/ping", key, nil)
		Expect(code).To(Equal(http.StatusOK), string(b))
		for path, want := range map[string]int{
			base + "/nope":           http.StatusNotFound,
			base + "/jobs/a/b":       http.StatusNotFound,
			base + "/jobs/%2e%2e":    http.StatusBadRequest,
			base + "/jobs/..%2fping": http.StatusBadRequest,
			// Not in the list of the key: refused before the add-on is looked up.
			"/api/client/addons/other/ping": http.StatusForbidden,
		} {
			code, _, _ = do(http.MethodGet, path, key, nil)
			Expect(code).To(Equal(want), path)
		}
		code, _, _ = do(http.MethodPut, base+"/ping", key, map[string]any{})
		Expect(code).To(Equal(http.StatusMethodNotAllowed))
		// Over the limit of the route: 4096 bytes.
		big := map[string]any{"task": "ask", "params": map[string]string{"q": strings.Repeat("a", 5000)}}
		code, _, _ = do(http.MethodPost, base+"/jobs", key, big)
		Expect(code).To(Equal(http.StatusRequestEntityTooLarge))
		// The add-on refuses a body it does not know.
		code, _, _ = do(http.MethodPost, base+"/jobs", key, map[string]any{"task": "ask", "user": "bob"})
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("limits a client to the add-ons that the owner chose, and the owner can change the list", func() {
		base := "/api/client/addons/" + addonID
		// A client paired for another add-on gets 403 on this one, and the
		// same answer as for an add-on that does not exist.
		other, id := pairFor([]string{"some-other-addon"}, auth.FeatureClientSelf, auth.FeatureClientAddons)
		code, b1, _ := do(http.MethodGet, base+"/ping", other, nil)
		Expect(code).To(Equal(http.StatusForbidden))
		code, b2, _ := do(http.MethodGet, "/api/client/addons/no-such-addon/ping", other, nil)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(b2).To(Equal(b1))
		code, b, _ := do(http.MethodGet, "/api/client/self", other, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(b)).To(ContainSubstring("some-other-addon"))

		// The owner gives it this add-on, and it works at once.
		code, b, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"addons": []string{addonID}})
		Expect(code).To(Equal(http.StatusOK), string(b))
		code, _, _ = do(http.MethodGet, base+"/ping", other, nil)
		Expect(code).To(Equal(http.StatusOK))
		// And takes it away: an empty list reaches nothing.
		code, _, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"addons": []string{}})
		Expect(code).To(Equal(http.StatusOK))
		code, _, _ = do(http.MethodGet, base+"/ping", other, nil)
		Expect(code).To(Equal(http.StatusForbidden))

		// A code for client_addons must name the add-ons.
		code, _, _ = do(http.MethodPost, "/api/client/pairing-codes", admin, map[string]any{"features": []string{auth.FeatureClientAddons}})
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})
