// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// proposalRuntime is a stand-in addon: one revision, an idempotent receipt
// journal, and an optional lost reply.
type proposalRuntime struct {
	mu        sync.Mutex
	data      uint64
	receipts  map[string]contract.Receipt
	applied   []string
	args      map[string]string
	loseReply bool
	// dropApply fails the next apply before the addon records anything, so
	// no receipt ever exists for it.
	dropApply bool
	// down makes the addon runtime unreachable, so the owner can dispatch
	// nothing.
	down bool
}

func newProposalRuntime() *proposalRuntime {
	return &proposalRuntime{data: 1, receipts: map[string]contract.Receipt{}, args: map[string]string{}}
}

func (r *proposalRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, nil
}

func (r *proposalRuntime) Preview(_ context.Context, e contract.Envelope) (contract.Preview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return contract.Preview{OperationID: e.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: r.data}, ExpiresAt: time.Now().Add(time.Hour), Summary: json.RawMessage(`{"count":2,"sample":["a","b"]}`)}, nil
}

func (r *proposalRuntime) Apply(_ context.Context, e contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := e.Request.OperationID
	r.applied = append(r.applied, id)
	r.args[id] = string(e.Request.Arguments)
	if prior, ok := r.receipts[id]; ok {
		return prior, nil
	}
	if r.dropApply {
		r.dropApply = false
		return contract.Receipt{}, errors.New("connection reset before the addon took it")
	}
	receipt := contract.Receipt{OperationID: id, State: contract.ReceiptRejected}
	if rev.Data == r.data {
		r.data++
		receipt = contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: rev.Schema, Data: r.data}}
	}
	r.receipts[id] = receipt
	if r.loseReply {
		r.loseReply = false
		return contract.Receipt{}, errors.New("reply lost")
	}
	return receipt, nil
}

func (r *proposalRuntime) Receipt(_ context.Context, id string) (contract.Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rc, ok := r.receipts[id]; ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: id, State: contract.ReceiptUnknown}, nil
}

func (r *proposalRuntime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errors.New("none")
}

func (r *proposalRuntime) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.applied...)
}

type proposalInstances struct{ rt *proposalRuntime }

func (p proposalInstances) Runtime(context.Context, string) (addonruntime.Instance, error) {
	p.rt.mu.Lock()
	down := p.rt.down
	p.rt.mu.Unlock()
	if down {
		return addonruntime.Instance{}, errors.New("addon runtime unavailable")
	}
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: p.rt}, nil
}

// owner is the real owner stack: policy store, proposal broker and the gateway
// that serves them, over a real account database.
type ownerStack struct {
	db      *gorm.DB
	cfg     *config.ApplicationConfig
	gateway *gateway.Gateway
	broker  *policy.Broker
	policy  *policy.Policy
	rt      *proposalRuntime
	key     policy.ResourceKey
	tokens  map[string]string
	cookies map[string]string
	mode    string
	owner   contract.Actor
}

func newOwnerStack(mode string, withAccounts bool) *ownerStack {
	s := &ownerStack{rt: newProposalRuntime(), tokens: map[string]string{}, cookies: map[string]string{}, mode: mode, cfg: &config.ApplicationConfig{}}
	s.cfg.Auth.APIKeyHMACSecret = "secret"
	if withAccounts {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		s.db = db
		for _, id := range []string{"alice", "bob"} {
			Expect(db.Create(&auth.User{ID: id, Role: auth.RoleAdmin, Status: auth.StatusActive}).Error).To(Succeed())
			token, _, err := auth.CreateAPIKey(db, id, "agent", auth.RoleAdmin, "secret", nil)
			Expect(err).NotTo(HaveOccurred())
			s.tokens[id] = token
			cookie, err := auth.CreateSession(db, id, "secret")
			Expect(err).NotTo(HaveOccurred())
			s.cookies[id] = cookie
		}
	}
	root := GinkgoT().TempDir()
	store, err := policy.OpenStore(filepath.Join(root, "policy"), policy.Mode(mode))
	Expect(err).NotTo(HaveOccurred())
	s.broker, err = policy.OpenBroker(filepath.Join(root, "proposals"), policy.BrokerOptions{Runtime: proposalInstances{s.rt}})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.broker.Close(); _ = store.Close() })
	revalidate := auth.AddonRevalidate(s.db, mode)
	s.policy = policy.New(store, policy.Options{Revalidate: revalidate, CreateAuthority: func(context.Context, contract.Actor, string) error { return nil }, Proposals: s.broker})
	s.broker.Attach(s.policy)
	owner := contract.Actor{UserID: "alice", CredentialID: "key:seed", Mode: mode}
	if mode == auth.AddonModeLocalOwner {
		owner = contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: mode}
	}
	if withAccounts && mode == auth.AddonModeNamedUser {
		var k auth.UserAPIKey
		Expect(s.db.Where("user_id = ?", "alice").First(&k).Error).To(Succeed())
		owner.CredentialID = "key:" + k.ID
	}
	s.owner = owner
	s.key, err = s.policy.Register(context.Background(), owner, "sample")
	Expect(err).NotTo(HaveOccurred())
	Expect(s.policy.Commit(context.Background(), owner, s.key)).To(Succeed())
	provider := s.policy.Gateway()
	s.gateway = &gateway.Gateway{Revalidate: revalidate, Admission: provider, Resolver: provider, Proposals: s.broker.Gateway()}
	return s
}

// frontend serves the proposal and dispatch routes with the same middleware
// chain as the registered routes.
func (s *ownerStack) frontend(g *gateway.Gateway) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(s.db, s.cfg))
	group := e.Group("/api/addons", auth.RequireFeature(s.db, auth.FeatureAddons), auth.CSRFMiddleware(), BrowserProtection())
	mode := WithOwnership(s.mode)
	group.POST("/dispatch", Dispatch(g, s.db, mode))
	group.POST("/relay", Relay(g, s.db, mode))
	group.POST("/proposals", Propose(g, s.db, mode))
	group.GET("/proposals", ListPendingProposals(g, s.db, mode))
	group.GET("/proposals/:id", GetProposal(g, s.db, mode))
	group.POST("/proposals/:id/confirm", ConfirmProposal(g, s.db, mode))
	group.POST("/proposals/:id/cancel", CancelProposal(g, s.db, mode))
	group.POST("/proposals/:id/reconcile", ReconcileProposal(g, s.db, mode))
	group.POST("/proposals/:id/resolve", ResolveProposal(g, s.db, mode))
	group.GET("/resources/:resourceId/proposals", ListUnresolvedProposals(g, s.db, mode))
	group.GET("/artifacts/:id", GetArtifact(g, s.db, mode))
	group.GET("/artifacts/:id/download", DownloadArtifact(g, s.db, mode))
	group.GET("/apps", ListApps(g, s.db, mode))
	group.GET("/apps/:id/icon", GetAppIcon(g, s.db, mode))
	group.POST("/apps/:id/start", StartApp(g, s.db, mode))
	group.POST("/apps/:id/stop", StopApp(g, s.db, mode))
	group.GET("/:id/update", GetUpdate(g, s.db, mode))
	group.POST("/:id/update/check", CheckUpdate(g, s.db, mode))
	group.POST("/:id/upgrade", UpgradeAddon(g, s.db, mode))
	group.GET("/:id/assets/:digest/*", GetAsset(g, s.db, mode))
	return e
}

// forwardingFrontends serves the same routes as two other frontends that reach
// the owner gateway only through the signed, pinned carrier.
func (s *ownerStack) forwardingFrontends(owner *gateway.Gateway) (*echo.Echo, *echo.Echo) {
	certs, roots := certificates()
	pins := map[string][32]byte{}
	keys := map[string]ed25519.PublicKey{}
	private := map[string]ed25519.PrivateKey{}
	for _, id := range []string{"owner", "front-a", "front-b"} {
		pins[id] = sha256.Sum256(certs[id].Certificate[0])
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		keys[id], private[id] = pub, priv
	}
	server := httptest.NewUnstartedServer(nil)
	carriers := map[string]*gateway.Carrier{}
	for _, id := range []string{"owner", "front-a", "front-b"} {
		c, err := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
		Expect(err).NotTo(HaveOccurred())
		carriers[id] = c
	}
	var err error
	server.Config.Handler, err = carriers["owner"].OwnerHandler(owner)
	Expect(err).NotTo(HaveOccurred())
	server.TLS = carriers["owner"].ServerTLS()
	server.StartTLS()
	DeferCleanup(server.Close)
	revalidate := auth.AddonRevalidate(s.db, s.mode)
	a := s.frontend(&gateway.Gateway{Forward: carriers["front-a"], Revalidate: revalidate})
	b := s.frontend(&gateway.Gateway{Forward: carriers["front-b"], Revalidate: revalidate})
	return a, b
}

// stubBroker answers every proposal call with a fixed error, as an owner
// whose state is known would.
type stubBroker struct {
	err   error
	calls int
}

func (b *stubBroker) fail() (gateway.Proposal, error) { b.calls++; return gateway.Proposal{}, b.err }
func (b *stubBroker) Propose(context.Context, contract.Actor, contract.Request) (gateway.Proposal, error) {
	return b.fail()
}
func (b *stubBroker) Confirm(context.Context, contract.Actor, string, string) (gateway.Proposal, error) {
	return b.fail()
}
func (b *stubBroker) Cancel(context.Context, contract.Actor, string) (gateway.Proposal, error) {
	return b.fail()
}
func (b *stubBroker) Get(context.Context, contract.Actor, string) (gateway.Proposal, error) {
	return b.fail()
}
func (b *stubBroker) Reconcile(context.Context, contract.Actor, string) (gateway.Proposal, error) {
	return b.fail()
}
func (b *stubBroker) Resolve(context.Context, contract.Actor, string, string, string) (gateway.Proposal, error) {
	return b.fail()
}
func (b *stubBroker) Pending(context.Context, contract.Actor) ([]gateway.Proposal, error) {
	b.calls++
	return nil, b.err
}

func (b *stubBroker) Unresolved(context.Context, contract.Actor, string) ([]gateway.BlockingProposal, error) {
	b.calls++
	return nil, b.err
}

type reply struct {
	code int
	body map[string]any
}

func (r reply) state() string {
	s, _ := r.body["state"].(string)
	return s
}

func (r reply) errType() string {
	m, _ := r.body["error"].(map[string]any)
	t, _ := m["type"].(string)
	return t
}

func (r reply) errBlockedBy() string {
	m, _ := r.body["error"].(map[string]any)
	t, _ := m["blockedBy"].(string)
	return t
}

func (r reply) errOperation() string {
	m, _ := r.body["error"].(map[string]any)
	t, _ := m["operationId"].(string)
	return t
}

func send(e http.Handler, method, path, body string, headers func(*http.Request)) reply {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	if headers != nil {
		headers(r)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	out := reply{code: w.Code, body: map[string]any{}}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func bearerOf(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// humanTab is what the host confirmation surface sends.
func humanTab(cookie, id string) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set(ConfirmHeader, id)
	}
}

func without(base func(*http.Request), header string) func(*http.Request) {
	return func(r *http.Request) { base(r); r.Header.Del(header) }
}

func with(base func(*http.Request), header, value string) func(*http.Request) {
	return func(r *http.Request) { base(r); r.Header.Set(header, value) }
}

func (s *ownerStack) proposeBody(op string) string {
	return `{"addonId":"sample","resourceId":"` + s.key.ResourceID + `","operationId":"` + op + `","generation":1,"action":"write","arguments":{"rows":[{"a":1},{"a":2}]}}`
}

var _ = Describe("proposal routes", Label("proposals"), func() {
	It("stores a proposal from an API key and lets only the proposer's browser session confirm it", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		e := s.frontend(s.gateway)
		p := send(e, "POST", "/api/addons/proposals", s.proposeBody("change-1"), bearerOf(s.tokens["alice"]))
		Expect(p.code).To(Equal(200), "%v", p.body)
		Expect(p.state()).To(Equal("pending"))
		digest, _ := p.body["digest"].(string)
		Expect(digest).NotTo(BeEmpty())
		Expect(p.body["effects"].(map[string]any)["count"]).To(BeEquivalentTo(2))
		confirm := `{"digest":"` + digest + `"}`
		path := "/api/addons/proposals/change-1/confirm"
		tab := humanTab(s.cookies["alice"], "change-1")

		// Nothing that is not a human at the confirmation surface can approve.
		key := func(r *http.Request) {
			bearerOf(s.tokens["alice"])(r)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("Sec-Fetch-Mode", "cors")
			r.Header.Set("Sec-Fetch-Dest", "empty")
			r.Header.Set(ConfirmHeader, "change-1")
		}
		for name, headers := range map[string]func(*http.Request){
			"an API key with browser headers":      key,
			"an API key alone":                     bearerOf(s.tokens["alice"]),
			"a session with a bearer key":          func(r *http.Request) { tab(r); bearerOf(s.tokens["alice"])(r) },
			"a session without the header":         without(tab, ConfirmHeader),
			"a session naming another proposal":    with(tab, ConfirmHeader, "change-2"),
			"a session marked as an MCP session":   with(tab, "Mcp-Session-Id", "abc"),
			"a session marked as a tool call":      with(tab, "X-LocalAI-Tool-Call", "1"),
			"a session marked as an agent":         with(tab, "X-LocalAI-Agent", "run-1"),
			"a cross-site page":                    with(tab, "Sec-Fetch-Site", "cross-site"),
			"a navigation":                         with(tab, "Sec-Fetch-Dest", "document"),
			"a request without fetch destination":  without(tab, "Sec-Fetch-Dest"),
			"another user's session":               humanTab(s.cookies["bob"], "change-1"),
			"a stored token cookie plus a session": func(r *http.Request) { tab(r); r.AddCookie(&http.Cookie{Name: "token", Value: s.tokens["alice"]}) },
		} {
			got := send(e, "POST", path, confirm, headers)
			Expect(got.code).To(BeElementOf(400, 403), name)
			Expect(s.rt.calls()).To(BeEmpty(), name)
		}
		// A bad body from the right surface is a client error, not an apply.
		Expect(send(e, "POST", path, `{}`, tab).code).To(Equal(400))
		Expect(send(e, "POST", path, `{"digest":"`+digest+`","arguments":{"rows":[]}}`, tab).code).To(Equal(400))
		Expect(send(e, "POST", path, `{"digest":"someone else's preview"}`, tab).code).To(Equal(403))
		Expect(s.rt.calls()).To(BeEmpty())
		got := send(e, "GET", "/api/addons/proposals/change-1", "", bearerOf(s.tokens["alice"]))
		Expect(got.code).To(Equal(200))
		Expect(got.state()).To(Equal("pending"))
		// Another account does not even see it.
		Expect(send(e, "GET", "/api/addons/proposals/change-1", "", bearerOf(s.tokens["bob"])).code).To(Equal(403))

		done := send(e, "POST", path, confirm, tab)
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.state()).To(Equal("applied"))
		Expect(done.body["receipt"].(map[string]any)["state"]).To(Equal("committed"))
		Expect(s.rt.calls()).To(Equal([]string{"change-1"}))
		Expect(s.rt.args["change-1"]).To(Equal(`{"rows":[{"a":1},{"a":2}]}`))
		// Single use: a repeated confirmation returns the receipt and applies nothing.
		again := send(e, "POST", path, confirm, tab)
		Expect(again.code).To(Equal(200))
		Expect(again.state()).To(Equal("applied"))
		Expect(s.rt.calls()).To(HaveLen(1))
	})

	It("cancels a proposal from the same chain and never lets typed dispatch or relay confirm", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		e := s.frontend(s.gateway)
		p := send(e, "POST", "/api/addons/proposals", s.proposeBody("change-1"), bearerOf(s.tokens["alice"]))
		Expect(p.code).To(Equal(200))
		for _, action := range []string{"confirm", "proposal-confirm", "propose", "proposal-propose", "apply", "write"} {
			body := `{"addonId":"sample","resourceId":"` + s.key.ResourceID + `","operationId":"change-1","generation":1,"action":"` + action + `","arguments":{}}`
			got := send(e, "POST", "/api/addons/dispatch", body, bearerOf(s.tokens["alice"]))
			Expect(got.code).To(Equal(403), action)
			got = send(e, "POST", "/api/addons/relay", `{"request":`+body+`,"kind":"json"}`, bearerOf(s.tokens["alice"]))
			Expect(got.code).To(Equal(403), action)
		}
		Expect(s.rt.calls()).To(BeEmpty())
		cancel := send(e, "POST", "/api/addons/proposals/change-1/cancel", ``, bearerOf(s.tokens["alice"]))
		Expect(cancel.code).To(Equal(200), "%v", cancel.body)
		Expect(cancel.state()).To(Equal("cancelled"))
		digest, _ := p.body["digest"].(string)
		got := send(e, "POST", "/api/addons/proposals/change-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], "change-1"))
		Expect(got.code).To(Equal(403))
		Expect(s.rt.calls()).To(BeEmpty())
	})

	It("denies sharing and deletion proposals and an unauthenticated caller", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		e := s.frontend(s.gateway)
		for _, action := range []string{"share", "delete", "read"} {
			body := `{"addonId":"sample","resourceId":"` + s.key.ResourceID + `","operationId":"x","generation":1,"action":"` + action + `","arguments":{}}`
			Expect(send(e, "POST", "/api/addons/proposals", body, bearerOf(s.tokens["alice"])).code).To(Equal(403), action)
		}
		Expect(send(e, "POST", "/api/addons/proposals", s.proposeBody("anon"), nil).code).To(Equal(401))
		Expect(send(e, "POST", "/api/addons/proposals", s.proposeBody("forged"), func(r *http.Request) { r.Header.Set("X-Forwarded-Actor", "alice") }).code).To(Equal(401))
		Expect(send(e, "POST", "/api/addons/proposals", `{"addonId":"sample","unknown":1}`, bearerOf(s.tokens["alice"])).code).To(Equal(400))
	})

	It("serves the local owner's confirmation only from a same-origin fetch of the host page", func() {
		s := newOwnerStack(auth.AddonModeLocalOwner, false)
		e := s.frontend(s.gateway)
		p := send(e, "POST", "/api/addons/proposals", s.proposeBody("local-1"), nil)
		Expect(p.code).To(Equal(200), "%v", p.body)
		digest, _ := p.body["digest"].(string)
		confirm := `{"digest":"` + digest + `"}`
		page := func(r *http.Request) {
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("Sec-Fetch-Mode", "cors")
			r.Header.Set("Sec-Fetch-Dest", "empty")
			r.Header.Set(ConfirmHeader, "local-1")
		}
		path := "/api/addons/proposals/local-1/confirm"
		for name, h := range map[string]func(*http.Request){
			"no fetch metadata":  nil,
			"an API credential":  with(page, "Authorization", "Bearer anything"),
			"an x-api-key":       with(page, "X-Api-Key", "anything"),
			"a tool call marker": with(page, "X-LocalAI-Tool-Call", "1"),
			"no confirm header":  without(page, ConfirmHeader),
			"a cross-site page":  with(page, "Sec-Fetch-Site", "cross-site"),
		} {
			Expect(send(e, "POST", path, confirm, h).code).To(BeElementOf(400, 401, 403), name)
			Expect(s.rt.calls()).To(BeEmpty(), name)
		}
		done := send(e, "POST", path, confirm, page)
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.state()).To(Equal("applied"))
	})

	It("reports a lost reply with the original operation from forwarding frontends and resolves it by receipt", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		a, b := s.forwardingFrontends(s.gateway)

		p := send(a, "POST", "/api/addons/proposals", s.proposeBody("lost-1"), bearerOf(s.tokens["alice"]))
		Expect(p.code).To(Equal(200), "%v", p.body)
		digest, _ := p.body["digest"].(string)
		// The same proposal, read through the other frontend.
		Expect(send(b, "GET", "/api/addons/proposals/lost-1", "", bearerOf(s.tokens["alice"])).state()).To(Equal("pending"))
		// Another account cannot read or confirm it through either frontend.
		Expect(send(b, "GET", "/api/addons/proposals/lost-1", "", bearerOf(s.tokens["bob"])).code).To(Equal(403))
		Expect(send(a, "POST", "/api/addons/proposals/lost-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["bob"], "lost-1")).code).To(Equal(403))
		// A key cannot confirm through a frontend either.
		Expect(send(a, "POST", "/api/addons/proposals/lost-1/confirm", `{"digest":"`+digest+`"}`, bearerOf(s.tokens["alice"])).code).To(Equal(403))
		Expect(s.rt.calls()).To(BeEmpty())

		s.rt.mu.Lock()
		s.rt.loseReply = true
		s.rt.mu.Unlock()
		lost := send(b, "POST", "/api/addons/proposals/lost-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], "lost-1"))
		Expect(lost.code).To(Equal(503))
		Expect(lost.errType()).To(Equal("outcome_unknown"))
		Expect(lost.errOperation()).To(Equal("lost-1"))
		Expect(s.rt.calls()).To(Equal([]string{"lost-1"}))
		// A different change on the same resource is blocked until it resolves.
		blocked := send(a, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["alice"]))
		Expect(blocked.code).To(Equal(403))
		// The resource's owner role is told which operation blocks it, from
		// every frontend.
		Expect(blocked.errType()).To(Equal("outcome_blocked"))
		Expect(blocked.errBlockedBy()).To(Equal("lost-1"))
		Expect(send(b, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["alice"])).errBlockedBy()).To(Equal("lost-1"))
		Expect(send(s.frontend(s.gateway), "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["alice"])).errBlockedBy()).To(Equal("lost-1"))
		// The lookup resolves it from either frontend, without applying again.
		resolved := send(a, "POST", "/api/addons/proposals/lost-1/reconcile", ``, bearerOf(s.tokens["alice"]))
		Expect(resolved.code).To(Equal(200), "%v", resolved.body)
		Expect(resolved.state()).To(Equal("applied"))
		Expect(s.rt.calls()).To(Equal([]string{"lost-1"}))
		Expect(send(b, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["alice"])).code).To(Equal(200))
	})

	It("lets only the resource owner's browser session abandon an unresolved outcome and then accepts writes", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		e := s.frontend(s.gateway)
		Expect(s.policy.SetGrant(context.Background(), s.owner, s.key, "bob", policy.RoleEditor)).To(Succeed())
		p := send(e, "POST", "/api/addons/proposals", s.proposeBody("stuck-1"), bearerOf(s.tokens["bob"]))
		Expect(p.code).To(Equal(200), "%v", p.body)
		digest, _ := p.body["digest"].(string)
		s.rt.mu.Lock()
		s.rt.dropApply = true
		s.rt.mu.Unlock()
		lost := send(e, "POST", "/api/addons/proposals/stuck-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["bob"], "stuck-1"))
		Expect(lost.code).To(Equal(503))
		Expect(lost.errType()).To(Equal("outcome_unknown"))
		Expect(send(e, "POST", "/api/addons/proposals/stuck-1/reconcile", ``, bearerOf(s.tokens["bob"])).state()).To(Equal("outcome-unknown"))
		Expect(send(e, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["bob"])).code).To(Equal(403))

		path := "/api/addons/proposals/stuck-1/resolve"
		body := `{"digest":"` + digest + `","reason":"the addon lost the call"}`
		for name, h := range map[string]func(*http.Request){
			"no credential":             nil,
			"the owner's API key":       bearerOf(s.tokens["alice"]),
			"the owner's key and tab":   with(humanTab(s.cookies["alice"], "stuck-1"), "Authorization", "Bearer "+s.tokens["alice"]),
			"no confirm header":         without(humanTab(s.cookies["alice"], "stuck-1"), ConfirmHeader),
			"another proposal's header": humanTab(s.cookies["alice"], "other"),
			"a tool call marker":        with(humanTab(s.cookies["alice"], "stuck-1"), "X-LocalAI-Tool-Call", "1"),
			"a cross-site page":         with(humanTab(s.cookies["alice"], "stuck-1"), "Sec-Fetch-Site", "cross-site"),
			"the editor's own tab":      humanTab(s.cookies["bob"], "stuck-1"),
		} {
			got := send(e, "POST", path, body, h)
			Expect(got.code).To(BeElementOf(400, 401, 403), name)
		}
		Expect(send(e, "GET", "/api/addons/proposals/stuck-1", "", bearerOf(s.tokens["bob"])).state()).To(Equal("outcome-unknown"))
		// The reason and the digest are required.
		tab := humanTab(s.cookies["alice"], "stuck-1")
		Expect(send(e, "POST", path, `{"digest":"`+digest+`"}`, tab).code).To(Equal(400))
		Expect(send(e, "POST", path, `{"reason":"x"}`, tab).code).To(Equal(400))
		Expect(send(e, "POST", path, `{"digest":"wrong","reason":"x"}`, tab).code).To(Equal(403))

		done := send(e, "POST", path, body, tab)
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.state()).To(Equal("abandoned-unknown"))
		resolution, _ := done.body["resolution"].(map[string]any)
		Expect(resolution["actorId"]).To(Equal("alice"))
		Expect(resolution["reason"]).To(Equal("the addon lost the call"))
		Expect(s.rt.calls()).To(Equal([]string{"stuck-1"}))
		Expect(send(e, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["bob"])).code).To(Equal(200))
		// A late receipt is recorded, not hidden.
		s.rt.mu.Lock()
		s.rt.receipts["stuck-1"] = contract.Receipt{OperationID: "stuck-1", State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 2}}
		s.rt.mu.Unlock()
		late := send(e, "POST", "/api/addons/proposals/stuck-1/reconcile", ``, bearerOf(s.tokens["bob"]))
		Expect(late.state()).To(Equal("applied"))
		Expect(late.body).To(HaveKey("resolution"))
		Expect(s.rt.calls()).To(Equal([]string{"stuck-1"}))
	})

	It("lets the owner role list an editor's unresolved proposal and abandon it by the listed digest from every frontend", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		own := s.frontend(s.gateway)
		a, b := s.forwardingFrontends(s.gateway)
		Expect(s.policy.SetGrant(context.Background(), s.owner, s.key, "bob", policy.RoleEditor)).To(Succeed())
		p := send(own, "POST", "/api/addons/proposals", s.proposeBody("stuck-1"), bearerOf(s.tokens["bob"]))
		Expect(p.code).To(Equal(200), "%v", p.body)
		digest, _ := p.body["digest"].(string)
		s.rt.mu.Lock()
		s.rt.dropApply = true
		s.rt.mu.Unlock()
		lost := send(own, "POST", "/api/addons/proposals/stuck-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["bob"], "stuck-1"))
		Expect(lost.errType()).To(Equal("outcome_unknown"))
		// The owner cannot read the proposal by ID: it is the editor's.
		Expect(send(own, "GET", "/api/addons/proposals/stuck-1", "", bearerOf(s.tokens["alice"])).code).To(Equal(403))

		path := "/api/addons/resources/" + s.key.ResourceID + "/proposals?state=unresolved"
		tab := humanTab(s.cookies["alice"], s.key.ResourceID)
		for name, e := range map[string]http.Handler{"owner": own, "front-a": a, "front-b": b} {
			got := send(e, "GET", path, "", tab)
			Expect(got.code).To(Equal(200), name+" %v", got.body)
			list, _ := got.body["proposals"].([]any)
			Expect(list).To(HaveLen(1), name)
			entry, _ := list[0].(map[string]any)
			Expect(entry["id"]).To(Equal("stuck-1"), name)
			Expect(entry["state"]).To(Equal("outcome-unknown"), name)
			Expect(entry["proposerId"]).To(Equal("bob"), name)
			Expect(entry["action"]).To(Equal("write"), name)
			Expect(entry["digest"]).To(Equal(digest), name)
			Expect(entry).To(HaveKey("ageSeconds"), name)
			Expect(entry["effects"].(map[string]any)["count"]).To(BeEquivalentTo(2), name)
			Expect(entry).NotTo(HaveKey("arguments"), name)
			Expect(entry).NotTo(HaveKey("actor"), name)
		}

		// Nobody else, no key, no other header and no other filter lists it.
		denied := map[string]func(*http.Request){
			"the editor's tab":     humanTab(s.cookies["bob"], s.key.ResourceID),
			"the owner's key":      bearerOf(s.tokens["alice"]),
			"another resource tab": humanTab(s.cookies["alice"], "other"),
			"a tool call marker":   with(tab, "X-LocalAI-Tool-Call", "1"),
			"a cross-site page":    with(tab, "Sec-Fetch-Site", "cross-site"),
			"no credential":        nil,
		}
		for _, e := range []http.Handler{own, a, b} {
			for name, h := range denied {
				Expect(send(e, "GET", path, "", h).code).To(BeElementOf(400, 401, 403), name)
			}
			Expect(send(e, "GET", "/api/addons/resources/"+s.key.ResourceID+"/proposals", "", tab).code).To(Equal(400))
			Expect(send(e, "GET", "/api/addons/resources/"+s.key.ResourceID+"/proposals?state=all", "", tab).code).To(Equal(400))
			Expect(send(e, "GET", "/api/addons/resources/nope/proposals?state=unresolved", "", humanTab(s.cookies["alice"], "nope")).code).To(Equal(403))
		}
		// The editor is an installation administrator here, and still not the
		// owner of the resource.
		Expect(send(own, "GET", path, "", humanTab(s.cookies["bob"], s.key.ResourceID)).code).To(Equal(403))

		// The listed ID and digest are what resolve needs.
		done := send(a, "POST", "/api/addons/proposals/stuck-1/resolve", `{"digest":"`+digest+`","reason":"the addon lost the call"}`, humanTab(s.cookies["alice"], "stuck-1"))
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.state()).To(Equal("abandoned-unknown"))
		after := send(b, "GET", path, "", tab)
		list, _ := after.body["proposals"].([]any)
		Expect(list).To(HaveLen(1))
		Expect(list[0].(map[string]any)["state"]).To(Equal("abandoned-unknown"))
		// The editor's own writes work again, and the editor was never told
		// another operation's ID by a denial.
		Expect(send(own, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["bob"])).code).To(Equal(200))
	})

	It("lists the caller's own pending proposals the same from the owner and every forwarding frontend", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		own := s.frontend(s.gateway)
		a, b := s.forwardingFrontends(s.gateway)
		Expect(s.policy.SetGrant(context.Background(), s.owner, s.key, "bob", policy.RoleEditor)).To(Succeed())
		Expect(send(own, "POST", "/api/addons/proposals", s.proposeBody("mine-1"), bearerOf(s.tokens["alice"])).code).To(Equal(200))
		Expect(send(own, "POST", "/api/addons/proposals", s.proposeBody("mine-2"), bearerOf(s.tokens["alice"])).code).To(Equal(200))
		Expect(send(own, "POST", "/api/addons/proposals", s.proposeBody("bobs-1"), bearerOf(s.tokens["bob"])).code).To(Equal(200))
		Expect(send(own, "POST", "/api/addons/proposals/mine-2/cancel", "", bearerOf(s.tokens["alice"])).code).To(Equal(200))
		for name, e := range map[string]http.Handler{"owner": own, "front-a": a, "front-b": b} {
			got := send(e, "GET", "/api/addons/proposals?state=pending", "", bearerOf(s.tokens["alice"]))
			Expect(got.code).To(Equal(200), name+" %v", got.body)
			list, _ := got.body["proposals"].([]any)
			Expect(list).To(HaveLen(1), name)
			entry, _ := list[0].(map[string]any)
			Expect(entry["id"]).To(Equal("mine-1"), name)
			Expect(entry["state"]).To(Equal("pending"), name)
			Expect(entry["digest"]).NotTo(BeEmpty(), name)
			Expect(entry["effects"].(map[string]any)["count"]).To(BeEquivalentTo(2), name)
			// The credential that proposed is never part of the view.
			Expect(entry).NotTo(HaveKey("actor"), name)
			// Another user sees only their own.
			other := send(e, "GET", "/api/addons/proposals?state=pending", "", bearerOf(s.tokens["bob"]))
			ids, _ := other.body["proposals"].([]any)
			Expect(ids).To(HaveLen(1), name)
			Expect(ids[0].(map[string]any)["id"]).To(Equal("bobs-1"), name)
			// A browser session and a key both list; nobody else does; the
			// filter is required.
			Expect(send(e, "GET", "/api/addons/proposals", "", bearerOf(s.tokens["alice"])).code).To(Equal(400), name)
			Expect(send(e, "GET", "/api/addons/proposals?state=all", "", bearerOf(s.tokens["alice"])).code).To(Equal(400), name)
			Expect(send(e, "GET", "/api/addons/proposals?state=pending", "", nil).code).To(BeElementOf(401, 403), name)
		}
	})

	It("tells an editor of a blocked write only that it is denied", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		own := s.frontend(s.gateway)
		a, _ := s.forwardingFrontends(s.gateway)
		Expect(s.policy.SetGrant(context.Background(), s.owner, s.key, "bob", policy.RoleEditor)).To(Succeed())
		p := send(own, "POST", "/api/addons/proposals", s.proposeBody("stuck-1"), bearerOf(s.tokens["alice"]))
		digest, _ := p.body["digest"].(string)
		s.rt.mu.Lock()
		s.rt.dropApply = true
		s.rt.mu.Unlock()
		Expect(send(own, "POST", "/api/addons/proposals/stuck-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], "stuck-1")).errType()).To(Equal("outcome_unknown"))
		for _, e := range []http.Handler{own, a} {
			got := send(e, "POST", "/api/addons/proposals", s.proposeBody("by-editor"), bearerOf(s.tokens["bob"]))
			Expect(got.code).To(Equal(403))
			Expect(got.errType()).To(Equal("addon_error"))
			Expect(got.errBlockedBy()).To(BeEmpty())
			Expect(got.body).NotTo(HaveKey("blockedBy"))
		}
	})

	It("returns the applied proposal when the confirmed operation committed", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		a, _ := s.forwardingFrontends(s.gateway)
		p := send(a, "POST", "/api/addons/proposals", s.proposeBody("done-1"), bearerOf(s.tokens["alice"]))
		digest, _ := p.body["digest"].(string)
		done := send(a, "POST", "/api/addons/proposals/done-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], "done-1"))
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.state()).To(Equal("applied"))
		// A repeat of the confirmation is the stored answer, not a new apply.
		again := send(a, "POST", "/api/addons/proposals/done-1/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], "done-1"))
		Expect(again.state()).To(Equal("applied"))
		Expect(s.rt.calls()).To(Equal([]string{"done-1"}))
	})

	It("answers an owner outage by the owner's state with the same type from every frontend", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		stub := &stubBroker{}
		ownerGateway := &gateway.Gateway{Revalidate: auth.AddonRevalidate(s.db, auth.AddonModeNamedUser), Proposals: stub}
		own := s.frontend(ownerGateway)
		a, b := s.forwardingFrontends(ownerGateway)
		calls := map[string]func(e http.Handler) reply{
			"propose": func(e http.Handler) reply {
				return send(e, "POST", "/api/addons/proposals", s.proposeBody("op-1"), bearerOf(s.tokens["alice"]))
			},
			"confirm": func(e http.Handler) reply {
				return send(e, "POST", "/api/addons/proposals/op-1/confirm", `{"digest":"d"}`, humanTab(s.cookies["alice"], "op-1"))
			},
			"cancel": func(e http.Handler) reply {
				return send(e, "POST", "/api/addons/proposals/op-1/cancel", ``, bearerOf(s.tokens["alice"]))
			},
			"resolve": func(e http.Handler) reply {
				return send(e, "POST", "/api/addons/proposals/op-1/resolve", `{"digest":"d","reason":"r"}`, humanTab(s.cookies["alice"], "op-1"))
			},
		}
		fronts := map[string]http.Handler{"owner": own, "front-a": a, "front-b": b}
		// Nothing was dispatched: the proposal call is unavailable, names the
		// proposal and says nothing about an unknown outcome.
		stub.err = gateway.ErrUnavailable
		for name, call := range calls {
			for front, e := range fronts {
				got := call(e)
				Expect(got.code).To(Equal(503), name+" "+front)
				Expect(got.errType()).To(Equal("proposal_unavailable"), name+" "+front)
				Expect(got.errOperation()).To(Equal("op-1"), name+" "+front)
			}
		}
		// The owner says the operation reached the addon: unknown outcome.
		stub.err = &gateway.OutcomeUnknown{OperationID: "op-1"}
		for name, call := range calls {
			for front, e := range fronts {
				got := call(e)
				Expect(got.code).To(Equal(503), name+" "+front)
				Expect(got.errType()).To(Equal("outcome_unknown"), name+" "+front)
				Expect(got.errOperation()).To(Equal("op-1"), name+" "+front)
			}
		}
		// A denial stays a denial everywhere.
		stub.err = gateway.ErrForbidden
		for name, call := range calls {
			for front, e := range fronts {
				Expect(call(e).code).To(Equal(403), name+" "+front)
			}
		}
	})

	It("keeps a proposal confirmable by the same ID after the owner could not reach the addon", func() {
		s := newOwnerStack(auth.AddonModeNamedUser, true)
		own := s.frontend(s.gateway)
		a, b := s.forwardingFrontends(s.gateway)
		// An ordered list: the assertion below reads the addon's call order.
		for _, front := range []struct {
			name string
			e    http.Handler
		}{{"owner", own}, {"front-a", a}, {"front-b", b}} {
			name, e := front.name, front.e
			id := "op-" + name
			p := send(own, "POST", "/api/addons/proposals", s.proposeBody(id), bearerOf(s.tokens["alice"]))
			Expect(p.code).To(Equal(200), "%v", p.body)
			digest, _ := p.body["digest"].(string)
			s.rt.mu.Lock()
			s.rt.down = true
			s.rt.mu.Unlock()
			down := send(e, "POST", "/api/addons/proposals/"+id+"/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], id))
			Expect(down.code).To(Equal(503), name)
			Expect(down.errType()).To(Equal("proposal_unavailable"), name)
			Expect(down.errOperation()).To(Equal(id), name)
			Expect(s.rt.calls()).NotTo(ContainElement(id), name)
			// The owner's state is still pending, and the same ID applies.
			s.rt.mu.Lock()
			s.rt.down = false
			s.rt.mu.Unlock()
			Expect(send(e, "GET", "/api/addons/proposals/"+id, "", bearerOf(s.tokens["alice"])).state()).To(Equal("pending"), name)
			done := send(e, "POST", "/api/addons/proposals/"+id+"/confirm", `{"digest":"`+digest+`"}`, humanTab(s.cookies["alice"], id))
			Expect(done.code).To(Equal(200), "%s %v", name, done.body)
			Expect(done.state()).To(Equal("applied"), name)
		}
		Expect(s.rt.calls()).To(Equal([]string{"op-owner", "op-front-a", "op-front-b"}))
	})

	It("documents the proposal routes and the proposal schema in the Swagger spec", func() {
		spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths       map[string]map[string]any `json:"paths"`
			Definitions map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"definitions"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		for path, method := range map[string]string{
			"/api/addons/proposals":                        "post",
			"/api/addons/proposals/{id}":                   "get",
			"/api/addons/proposals/{id}/confirm":           "post",
			"/api/addons/proposals/{id}/cancel":            "post",
			"/api/addons/proposals/{id}/reconcile":         "post",
			"/api/addons/proposals/{id}/resolve":           "post",
			"/api/addons/resources/{resourceId}/proposals": "get",
		} {
			Expect(doc.Paths).To(HaveKey(path))
			Expect(doc.Paths[path]).To(HaveKey(method), path)
		}
		// The caller's own pending proposals have a documented read.
		Expect(doc.Paths["/api/addons/proposals"]).To(HaveKey("get"))
		pending, _ := json.Marshal(doc.Paths["/api/addons/proposals"]["get"])
		Expect(string(pending)).To(ContainSubstring("gateway.PendingList"))
		Expect(string(pending)).To(ContainSubstring("pending"))
		Expect(doc.Definitions["gateway.PendingList"].Properties).To(HaveKey("proposals"))
		var b []byte
		confirm, _ := json.Marshal(doc.Paths["/api/addons/proposals/{id}/confirm"])
		Expect(string(confirm)).To(ContainSubstring("X-LocalAI-Confirm"))
		Expect(string(confirm)).To(ContainSubstring("no MCP tool"))
		list, _ := json.Marshal(doc.Paths["/api/addons/resources/{resourceId}/proposals"])
		Expect(string(list)).To(ContainSubstring("X-LocalAI-Confirm"))
		Expect(string(list)).To(ContainSubstring("no MCP tool"))
		Expect(string(list)).To(ContainSubstring("gateway.ProposalList"))
		// Every proposal call that can end in an outage documents the typed
		// 503 bodies it can emit, and resolve documents only the one it does.
		for _, path := range []string{"/api/addons/proposals", "/api/addons/proposals/{id}/cancel", "/api/addons/proposals/{id}/confirm"} {
			text, _ := json.Marshal(doc.Paths[path])
			Expect(string(text)).To(ContainSubstring("outcome_unknown"), path)
			Expect(string(text)).To(ContainSubstring("proposal_unavailable"), path)
			Expect(string(text)).NotTo(ContainSubstring("did not reach the addon"), path)
		}
		resolveDoc, _ := json.Marshal(doc.Paths["/api/addons/proposals/{id}/resolve"])
		Expect(string(resolveDoc)).To(ContainSubstring("proposal_unavailable"))
		Expect(string(resolveDoc)).NotTo(ContainSubstring("Type outcome_unknown"))
		blocking := doc.Definitions["gateway.BlockingProposal"]
		b, err = json.Marshal(gateway.BlockingProposal{Reason: "r", Resolution: &gateway.ProposalResolution{}})
		Expect(err).NotTo(HaveOccurred())
		var shown map[string]any
		Expect(json.Unmarshal(b, &shown)).To(Succeed())
		for field := range shown {
			Expect(blocking.Properties).To(HaveKey(field), field)
		}
		Expect(blocking.Properties).NotTo(HaveKey("arguments"))
		Expect(doc.Definitions["addons.AddonError"].Properties).To(HaveKey("blockedBy"))
		proposal := doc.Definitions["gateway.Proposal"]
		for _, field := range []string{"id", "operationId", "arguments", "revision", "digest", "effects", "state", "expiresAt", "receipt", "blocked"} {
			Expect(proposal.Properties).To(HaveKey(field))
		}
		// Every response field the proposal view emits is documented.
		b, err = json.Marshal(gateway.Proposal{Receipt: &contract.Receipt{}, Blocked: true, Reason: "y"})
		Expect(err).NotTo(HaveOccurred())
		var emitted map[string]any
		Expect(json.Unmarshal(b, &emitted)).To(Succeed())
		for field := range emitted {
			Expect(proposal.Properties).To(HaveKey(field), strings.ToLower(field))
		}
	})
})
