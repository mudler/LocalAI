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
	group.GET("/proposals/:id", GetProposal(g, s.db, mode))
	group.POST("/proposals/:id/confirm", ConfirmProposal(g, s.db, mode))
	group.POST("/proposals/:id/cancel", CancelProposal(g, s.db, mode))
	group.POST("/proposals/:id/reconcile", ReconcileProposal(g, s.db, mode))
	return e
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
		server.Config.Handler, err = carriers["owner"].OwnerHandler(s.gateway)
		Expect(err).NotTo(HaveOccurred())
		server.TLS = carriers["owner"].ServerTLS()
		server.StartTLS()
		DeferCleanup(server.Close)
		revalidate := auth.AddonRevalidate(s.db, auth.AddonModeNamedUser)
		a := s.frontend(&gateway.Gateway{Forward: carriers["front-a"], Revalidate: revalidate})
		b := s.frontend(&gateway.Gateway{Forward: carriers["front-b"], Revalidate: revalidate})

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
		// The lookup resolves it from either frontend, without applying again.
		resolved := send(a, "POST", "/api/addons/proposals/lost-1/reconcile", ``, bearerOf(s.tokens["alice"]))
		Expect(resolved.code).To(Equal(200), "%v", resolved.body)
		Expect(resolved.state()).To(Equal("applied"))
		Expect(s.rt.calls()).To(Equal([]string{"lost-1"}))
		Expect(send(b, "POST", "/api/addons/proposals", s.proposeBody("next-1"), bearerOf(s.tokens["alice"])).code).To(Equal(200))
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
			"/api/addons/proposals":                "post",
			"/api/addons/proposals/{id}":           "get",
			"/api/addons/proposals/{id}/confirm":   "post",
			"/api/addons/proposals/{id}/cancel":    "post",
			"/api/addons/proposals/{id}/reconcile": "post",
		} {
			Expect(doc.Paths).To(HaveKey(path))
			Expect(doc.Paths[path]).To(HaveKey(method), path)
		}
		confirm, _ := json.Marshal(doc.Paths["/api/addons/proposals/{id}/confirm"])
		Expect(string(confirm)).To(ContainSubstring("X-LocalAI-Confirm"))
		Expect(string(confirm)).To(ContainSubstring("no MCP tool"))
		proposal := doc.Definitions["gateway.Proposal"]
		for _, field := range []string{"id", "operationId", "arguments", "revision", "digest", "effects", "state", "expiresAt", "receipt", "blockedBy"} {
			Expect(proposal.Properties).To(HaveKey(field))
		}
		// Every response field the proposal view emits is documented.
		b, err := json.Marshal(gateway.Proposal{Receipt: &contract.Receipt{}, BlockedBy: "x", Reason: "y"})
		Expect(err).NotTo(HaveOccurred())
		var emitted map[string]any
		Expect(json.Unmarshal(b, &emitted)).To(Succeed())
		for field := range emitted {
			Expect(proposal.Properties).To(HaveKey(field), strings.ToLower(field))
		}
	})
})
