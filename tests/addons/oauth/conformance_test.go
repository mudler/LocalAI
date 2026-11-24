// SPDX-License-Identifier: MIT
//go:build auth

package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// person is alice using the add-on of one service through one node.
type person struct {
	c       *cluster
	s       service
	p       *oauthtest.Provider
	n       *node
	binding string
	chat    string
	// texts collects every answer the person and the model saw.
	mu    sync.Mutex
	texts []string
}

func (c *cluster) person(s service, n *node) *person {
	return &person{c: c, s: s, p: c.providers[s.AddonID], n: n}
}

func (u *person) keep(s string) string {
	u.texts = append(u.texts, s)
	return s
}

// approve saves the client of the add-on as the administrator, which approves
// the descriptor the form shows.
func (u *person) approve() map[string]any {
	rec := u.n.do("root", http.MethodGet, "/api/chats/admin/oauth-clients/"+u.s.AddonID, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	got := decode(rec)
	body := map[string]any{"client_id": u.s.ClientID, "descriptor_digest": got["current_digest"]}
	if u.s.ClientSecret != "" {
		body["client_secret"] = u.s.ClientSecret
	}
	rec = u.n.do("root", http.MethodPut, "/api/chats/admin/oauth-clients/"+u.s.AddonID, body)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	saved := decode(rec)
	ExpectWithOffset(1, saved["approved"]).To(BeTrue())
	u.keep(rec.Body.String())
	return saved
}

// connection creates the person's OAuth binding and a chat.
func (u *person) connection() {
	rec := u.n.do("alice", http.MethodPost, "/api/chats/connectors", map[string]any{"kind": u.s.Kind, "name": "Work", "auth_kind": "oauth2"})
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
	u.binding = decode(rec)["id"].(string)
	// A chat needs a model to be created through its route; the tool call
	// needs only the record.
	u.chat = "chat-" + u.s.AddonID
	now := time.Now()
	ExpectWithOffset(1, u.c.owner.app.StudioStore().CreateChat(context.Background(), session.ChatRecord{ID: u.chat, Principal: "alice", Title: "Work", Created: now, Updated: now})).To(Succeed())
}

// start begins a login on node n and returns the authorize address and the
// nonce cookie.
func (u *person) start(n *node, optional []string) (*url.URL, *http.Cookie) {
	rec := n.do("alice", http.MethodPost, "/api/chats/connectors/"+u.binding+"/oauth/start", map[string]any{"scopes": optional, "return_to": returnTo})
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	u.keep(rec.Body.String())
	authorize, err := url.Parse(decode(rec)["authorize_url"].(string))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	var nonce *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if strings.HasPrefix(ck.Name, "localai_oauth_nonce_") {
			nonce = &http.Cookie{Name: ck.Name, Value: ck.Value}
		}
	}
	ExpectWithOffset(1, nonce).NotTo(BeNil())
	return authorize, nonce
}

// visit plays the browser at the service and returns where it sends the
// person back.
func (u *person) visit(authorize *url.URL) *url.URL {
	back, err := u.c.network.Authorize(context.Background(), authorize.String())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, back.Scheme+"://"+back.Host+back.Path).To(Equal(callbackURL))
	return back
}

// arrive sends the browser back to the callback on node n and returns the
// page it ends on.
func (u *person) arrive(n *node, back *url.URL, nonce *http.Cookie) *url.URL {
	rec := n.do("alice", http.MethodGet, back.RequestURI(), nil, nonce)
	u.keep(rec.Header().Get("Location"))
	return location(rec)
}

// connect runs the whole login and expects it to succeed.
func (u *person) connect(optional []string) {
	authorize, nonce := u.start(u.n, optional)
	page := u.arrive(u.n, u.visit(authorize), nonce)
	ExpectWithOffset(1, page.Path).To(Equal(returnTo))
	ExpectWithOffset(1, page.Query().Get("oauth")).To(Equal("ok"), page.String())
}

// oauth is the login state of the binding as the person's node reports it.
func (u *person) oauth(n *node) map[string]any {
	rec := n.do("alice", http.MethodGet, "/api/chats/connectors/"+u.binding, nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	u.keep(rec.Body.String())
	return decode(rec)["oauth"].(map[string]any)
}

// answer is what the model reads after one tool call, and the add-on's
// payload when it answered.
type answer struct {
	text    string
	result  tools.ToolResult
	payload map[string]any
}

// call runs the add-on's tool in the person's chat on node n, as a model
// would.
func (u *person) call(n *node, reject ...string) answer {
	a, err := u.try(n, reject...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return a
}

// try is call that returns the error of a call the host could not make.
func (u *person) try(n *node, reject ...string) (answer, error) {
	broker := n.app.StudioAddonTools()
	ExpectWithOffset(2, broker).NotTo(BeNil())
	args, _ := json.Marshal(map[string]any{"reject": reject})
	if len(reject) == 0 {
		args = json.RawMessage(`{}`)
	}
	text, err := broker.Call(context.Background(), "alice", u.chat, "addon__"+u.s.AddonID+"__whoami", args)
	if err != nil {
		return answer{}, err
	}
	u.mu.Lock()
	u.keep(text)
	u.mu.Unlock()
	var a answer
	a.text = text
	ExpectWithOffset(2, json.Unmarshal([]byte(text), &a.result)).To(Succeed(), text)
	if len(a.result.Result) > 0 {
		ExpectWithOffset(2, json.Unmarshal(a.result.Result, &a.payload)).To(Succeed(), text)
	}
	return a, nil
}

// expectServed checks that the add-on answered with the access token the
// service issued last and the scopes, and that it saw nothing else secret.
func (u *person) expectServed(a answer, scopes []string) {
	ExpectWithOffset(1, a.result.Kind).To(Equal(tools.ResultData), a.text)
	ExpectWithOffset(1, a.payload["addon"]).To(Equal(u.s.AddonID))
	ExpectWithOffset(1, a.payload["credential"]).To(Equal(hash(u.p.LastAccess())), "the add-on got the current access token")
	ExpectWithOffset(1, a.payload["grantedScopes"]).To(ConsistOf(toAny(scopes)...))
	req := a.payload["request"].(map[string]any)
	keys := []string{}
	for k := range req {
		keys = append(keys, k)
	}
	ExpectWithOffset(1, keys).To(ConsistOf("addonId", "operationId", "generation", "action", "arguments", "credential", "grantedScopes"),
		"the request carries the access token and the scopes and no other host-set secret")
	u.expectNoSecret(a.text)
}

// expectNoSecret checks that text holds no refresh token, client secret,
// code, verifier, state or service error text, and no access token in clear.
func (u *person) expectNoSecret(text string) {
	secrets := []string{oauthtest.Sentinel}
	if u.s.ClientSecret != "" {
		secrets = append(secrets, u.s.ClientSecret)
	}
	secrets = append(secrets, u.p.Issued()...)
	for _, call := range u.p.Calls() {
		for _, k := range []string{"code_verifier", "code", "refresh_token"} {
			if v := call.Form.Get(k); v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	for _, s := range secrets {
		ExpectWithOffset(2, text).NotTo(ContainSubstring(s))
	}
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// tokenCalls are the calls of the token endpoint since from.
func (u *person) tokenCalls(from int) []oauthtest.Call {
	var out []oauthtest.Call
	for _, call := range u.p.Calls()[from:] {
		if call.Endpoint == "token" {
			out = append(out, call)
		}
	}
	return out
}

// expectClientAuth checks the client authentication of every call on the
// wire: the one per-service difference the host must get right from the
// descriptor alone.
func (u *person) expectClientAuth() {
	calls := u.p.Calls()
	ExpectWithOffset(1, calls).NotTo(BeEmpty())
	for _, call := range calls {
		ExpectWithOffset(1, call.Auth).To(Equal(u.s.ClientAuth), "%s call to %s", call.Endpoint, call.Host)
		if u.s.ClientAuth != contract.OAuthClientAuthPost {
			ExpectWithOffset(1, call.Form).NotTo(HaveKey("client_secret"))
		}
	}
}

var _ = Describe("OAuth logins of add-ons, the same for every service", func() {
	var c *cluster
	BeforeEach(func() { c = newCluster() })

	// entries runs a table once for every service. The name of an entry
	// starts with the table's own word, so every case of the suite has a
	// name of its own in the inventory.
	entries := func(table string) []TableEntry {
		var out []TableEntry
		for _, s := range services {
			out = append(out, Entry(table+": "+s.AddonID+" ("+s.ClientAuth+" client authentication)", s))
		}
		return out
	}

	DescribeTable("logs in, serves calls, renews and ends the login", func(s service) {
		u := c.person(s, c.owner)
		saved := u.approve()
		Expect(saved["has_secret"]).To(Equal(s.ClientSecret != ""))
		Expect(saved["descriptor"].(map[string]any)["client_auth"]).To(Equal(s.ClientAuth))
		Expect(saved["callback_url"]).To(Equal(callbackURL))
		u.connection()
		Expect(u.oauth(c.owner)["state"]).To(Equal("not_connected"))

		By("starting a login, which sends the browser to the service with what the descriptor says")
		authorize, nonce := u.start(c.owner, s.Optional)
		q := authorize.Query()
		Expect(authorize.Host).To(Equal(s.AuthorizeHost))
		Expect(q.Get("client_id")).To(Equal(s.ClientID))
		Expect(q.Get("redirect_uri")).To(Equal(callbackURL))
		Expect(strings.Fields(q.Get("scope"))).To(ConsistOf(toAny(s.scopes())...))
		Expect(q.Get("code_challenge_method")).To(Equal("S256"))
		for _, p := range s.Params {
			Expect(q.Get(p.Name)).To(Equal(p.Value), p.Name)
		}

		By("coming back to the callback, which trades the code once and connects the binding")
		page := u.arrive(c.owner, u.visit(authorize), nonce)
		Expect(page.Query().Get("oauth")).To(Equal("ok"))
		Expect(u.tokenCalls(0)).To(HaveLen(1))
		Expect(u.tokenCalls(0)[0].Host).To(Equal(s.TokenHost))
		o := u.oauth(c.owner)
		Expect(o["state"]).To(Equal("connected"))
		Expect(o["account_label"]).To(Equal("person@" + s.AddonID + ".example.test"))
		Expect(o["scopes"]).To(ConsistOf(toAny(s.scopes())...))

		By("calling the tool, which gets the access token and the scopes and nothing else")
		a := u.call(c.owner)
		u.expectServed(a, s.scopes())
		me := []any{}
		for _, raw := range a.payload["connector"].([]any) {
			if call := raw.(map[string]any); call["op"] == contract.ConnectorOpMe {
				me = append(me, call["token"])
			}
		}
		Expect(me).To(Equal([]any{"sha256:" + hash(u.p.LastAccess())}), "the login asked the add-on who the person is with the new access token")
		Expect(u.tokenCalls(0)).To(HaveLen(1), "a fresh token is not refreshed")

		By("ending the access token: many calls at once refresh it once, and the rotated refresh token is kept")
		u.p.Set(func(p *oauthtest.Provider) { p.Rotate = true })
		firstRefresh := ""
		for _, call := range u.tokenCalls(0) {
			firstRefresh = call.Form.Get("refresh_token")
		}
		Expect(firstRefresh).To(BeEmpty(), "the login traded a code")
		before := len(u.p.Calls())
		c.clock.Advance(time.Hour)
		// The held answer keeps every caller waiting on the one refresh.
		gate := make(chan struct{})
		tokensBefore := u.p.Tokens.Load()
		u.p.Set(func(p *oauthtest.Provider) { p.Gate = gate })
		var wg sync.WaitGroup
		answers := make([]answer, 12)
		errs := make([]error, len(answers))
		for i := range answers {
			wg.Go(func() {
				defer GinkgoRecover()
				answers[i], errs[i] = u.try(c.owner)
			})
		}
		Eventually(u.p.Tokens.Load).Should(Equal(tokensBefore+1), "the one refresh is at the provider")
		close(gate)
		wg.Wait()
		u.p.Set(func(p *oauthtest.Provider) { p.Gate = nil })
		Expect(u.tokenCalls(before)).To(HaveLen(1), "one refresh for every call that found the token ended")
		served := 0
		for i, a := range answers {
			if errs[i] != nil {
				// The add-on serves contract.DefaultLimits().Concurrency calls
				// at a time and the host refuses the rest unsent.
				Expect(errs[i]).To(MatchError(addonruntime.ErrNotSent))
				continue
			}
			served++
			u.expectServed(a, s.scopes())
		}
		Expect(served).To(BeNumerically(">=", 1))
		rotatedFrom := u.tokenCalls(before)[0].Form.Get("refresh_token")
		Expect(rotatedFrom).NotTo(BeEmpty())
		Expect(u.p.Live(rotatedFrom)).To(BeFalse(), "the service rotated the refresh token")

		By("rejecting the token in the add-on: the host renews it once and the call is retried")
		before = len(u.p.Calls())
		a = u.call(c.owner, hash(u.p.LastAccess()))
		Expect(u.tokenCalls(before)).To(HaveLen(1))
		Expect(u.tokenCalls(before)[0].Form.Get("refresh_token")).NotTo(Equal(rotatedFrom), "the rotated refresh token was stored and used")
		u.expectServed(a, s.scopes())

		By("rejecting the renewed token too: the login ends and the model is told to have the person reconnect")
		before = len(u.p.Calls())
		a = u.call(c.owner, "all")
		Expect(u.tokenCalls(before)).To(HaveLen(1), "one renewal, no more")
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.result.Code).To(Equal(tools.CodeReconnectNeeded))
		Expect(a.result.Note).To(Equal(tools.ReconnectNeededNote))
		o = u.oauth(c.owner)
		Expect(o["state"]).To(Equal("reconnect_needed"))
		Expect(o["reason"]).To(Equal("credential_rejected"))
		before = len(u.p.Calls())
		Expect(u.call(c.owner).result.Code).To(Equal(tools.CodeReconnectNeeded), "an ended login is refused before the add-on runs")
		Expect(u.p.Calls()[before:]).To(BeEmpty())

		By("reconnecting, then the service refusing the refresh token with invalid_grant")
		u.connect(nil)
		u.expectServed(u.call(c.owner), s.Required)
		u.p.RevokeAll()
		c.clock.Advance(time.Hour)
		a = u.call(c.owner)
		Expect(a.result.Code).To(Equal(tools.CodeReconnectNeeded), a.text)
		Expect(a.result.Note).To(Equal(tools.ReconnectNeededNote))
		Expect(u.oauth(c.owner)["reason"]).To(Equal("revoked"))

		By("declining at the service, which leaves the login as it was")
		u.connect(s.Optional)
		accessBefore := u.p.LastAccess()
		before = len(u.p.Calls())
		u.p.Set(func(p *oauthtest.Provider) { p.Deny = true })
		authorize, nonce = u.start(c.owner, nil)
		page = u.arrive(c.owner, u.visit(authorize), nonce)
		Expect(page.String()).To(Equal(returnTo + "?oauth=denied"))
		u.p.Set(func(p *oauthtest.Provider) { p.Deny = false })
		Expect(u.p.Calls()[before:]).To(BeEmpty(), "a denial trades nothing")
		o = u.oauth(c.owner)
		Expect(o["state"]).To(Equal("connected"))
		Expect(o["scopes"]).To(ConsistOf(toAny(s.scopes())...))
		Expect(u.call(c.owner).payload["credential"]).To(Equal(hash(accessBefore)))

		By("disconnecting, which revokes at the service and deletes the login")
		refresh := ""
		for _, call := range u.tokenCalls(0) {
			if r := call.Form.Get("refresh_token"); r != "" {
				refresh = r
			}
		}
		live := ""
		for _, v := range u.p.Issued() {
			if strings.Contains(v, "-refresh-") && u.p.Live(v) {
				live = v
			}
		}
		Expect(live).NotTo(BeEmpty())
		Expect(refresh).NotTo(Equal(live), "the last refresh token came from the last login")
		before = len(u.p.Calls())
		rec := c.owner.do("alice", http.MethodPost, "/api/chats/connectors/"+u.binding+"/oauth/disconnect", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decode(rec)).To(Equal(map[string]any{"revoked": true}))
		revokes := u.p.Calls()[before:]
		Expect(revokes).To(HaveLen(1))
		Expect(revokes[0].Endpoint).To(Equal("revoke"))
		Expect(revokes[0].Form.Get("token")).To(Equal(live))
		Expect(revokes[0].Form.Get("token_type_hint")).To(Equal("refresh_token"))
		Expect(u.p.Live(live)).To(BeFalse())
		Expect(u.oauth(c.owner)["state"]).To(Equal("not_connected"))
		Expect(u.call(c.owner).result.Code).To(Equal(tools.CodeNoCredential))

		Expect(c.logText()).To(ContainSubstring("an OAuth connection needs a reconnect"), "the spec saw the logs")

		By("having authenticated the client the way the descriptor says on every call")
		u.expectClientAuth()
		for _, t := range append(u.texts, c.logText()) {
			u.expectNoSecret(t)
		}
	}, entries("login"))

	DescribeTable("keeps the login through an outage, a rate limit and a scope downgrade", func(s service) {
		u := c.person(s, c.owner)
		u.approve()
		u.connection()

		By("refusing a login whose service grants fewer than the required scopes")
		u.p.Set(func(p *oauthtest.Provider) { p.Grant = []string{"unrelated"} })
		authorize, nonce := u.start(c.owner, nil)
		page := u.arrive(c.owner, u.visit(authorize), nonce)
		Expect(page.Query().Get("class")).To(Equal("scopes_missing"))
		Expect(u.oauth(c.owner)["state"]).To(Equal("not_connected"))

		By("keeping the scopes the service granted when it grants fewer than asked")
		u.p.Set(func(p *oauthtest.Provider) { p.Grant = s.Required })
		u.connect(s.Optional)
		Expect(u.oauth(c.owner)["scopes"]).To(ConsistOf(toAny(s.Required)...))
		u.expectServed(u.call(c.owner), s.Required)

		By("reporting a 5xx of the token endpoint as an outage and keeping the login")
		u.p.Set(func(p *oauthtest.Provider) { p.Fail, p.FailStatus = "server_error", http.StatusServiceUnavailable })
		c.clock.Advance(time.Hour)
		before := len(u.p.Calls())
		_, err := u.try(c.owner)
		Expect(err).To(MatchError(gateway.ErrUnavailable), "an outage, as an outage of the add-on is")
		Expect(u.tokenCalls(before)).To(HaveLen(1))
		Expect(u.oauth(c.owner)["state"]).To(Equal("connected"))

		By("reporting a 429 as rate limited with its wait, and keeping the login")
		u.p.Set(func(p *oauthtest.Provider) {
			p.Fail, p.FailStatus, p.RetryAfter = "slow_down", http.StatusTooManyRequests, "7"
		})
		a := u.call(c.owner)
		Expect(a.result.Code).To(Equal(tools.CodeRateLimited), a.text)
		Expect(a.result.RetryAfter).To(Equal(7))
		Expect(u.oauth(c.owner)["state"]).To(Equal("connected"))

		By("working again when the service is back")
		u.p.Set(func(p *oauthtest.Provider) { p.Fail, p.FailStatus, p.RetryAfter = "", 0, "" })
		u.expectServed(u.call(c.owner), s.Required)
		u.expectClientAuth()
		for _, t := range append(u.texts, c.logText()) {
			u.expectNoSecret(t)
		}
	}, entries("failures"))

	DescribeTable("uses a short-lived token for most of its life before it renews it", func(s service) {
		u := c.person(s, c.owner)
		u.approve()
		u.connection()
		// One minute is the shortest lifetime the host accepts from a service.
		u.p.Set(func(p *oauthtest.Provider) { p.ExpiresIn, p.Rotate = 60, true })
		u.connect(nil)
		u.expectServed(u.call(c.owner), s.Required)
		u.expectServed(u.call(c.owner), s.Required)
		Expect(u.tokenCalls(0)).To(HaveLen(1), "a token the service just issued is used, not renewed at once")
		c.clock.Advance(50 * time.Second)
		u.expectServed(u.call(c.owner), s.Required)
		u.expectServed(u.call(c.owner), s.Required)
		Expect(u.tokenCalls(0)).To(HaveLen(2), "a token near its end is renewed once")
		c.clock.Advance(20 * time.Second)
		u.expectServed(u.call(c.owner), s.Required)
		Expect(u.tokenCalls(0)).To(HaveLen(2), "the renewed token is fresh again")
		u.expectClientAuth()
	}, entries("short token"))

	DescribeTable("voids the approval when an upgrade changes the descriptor, and never sends the refresh token to the new address", func(s service) {
		u := c.person(s, c.owner)
		u.approve()
		u.connection()
		u.connect(nil)
		u.expectServed(u.call(c.owner), s.Required)

		ctx := context.Background()
		manager := c.owner.app.AddonManager()
		_, err := manager.Submit(ctx, c.admin, addonUpgrade(s.AddonID))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string {
			rec := c.owner.do("root", http.MethodGet, "/api/chats/admin/oauth-clients/"+s.AddonID, nil)
			if rec.Code != http.StatusOK {
				return ""
			}
			return decode(rec)["descriptor"].(map[string]any)["token_url"].(string)
		}, 60*time.Second, 100*time.Millisecond).Should(Equal("https://" + s.MovedTokenHost + "/token"))
		rec := c.owner.do("root", http.MethodGet, "/api/chats/admin/oauth-clients/"+s.AddonID, nil)
		Expect(decode(rec)["approved"]).To(BeFalse(), "a changed descriptor ends the approval")

		before := len(u.p.Calls())
		c.clock.Advance(time.Hour)
		a := u.call(c.owner)
		Expect(a.result.Code).To(Equal(tools.CodeReconnectNeeded), a.text)
		Expect(u.oauth(c.owner)["reason"]).To(Equal("needs_approval"))
		Expect(u.p.Calls()[before:]).To(BeEmpty(), "no refresh token went anywhere")
		rec = c.owner.do("alice", http.MethodPost, "/api/chats/connectors/"+u.binding+"/oauth/start", map[string]any{"return_to": returnTo})
		Expect(rec.Code).To(Equal(http.StatusConflict), "no login before the administrator approves again")

		By("logging in at the new address once the administrator approved it")
		u.approve()
		u.connect(nil)
		calls := u.tokenCalls(before)
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].Host).To(Equal(s.MovedTokenHost))
		Expect(calls[0].Form.Get("grant_type")).To(Equal("authorization_code"))
		for _, call := range u.p.Calls() {
			if call.Host == s.MovedTokenHost {
				Expect(call.Form.Get("refresh_token")).To(BeEmpty(), "a refresh token of the old descriptor at the new address")
			}
		}
		u.expectServed(u.call(c.owner), s.Required)
		u.expectClientAuth()
	}, entries("upgrade"))

	DescribeTable("finishes on the owner a login whose callback reached a frontend", func(s service) {
		front := c.frontend()
		_, err := front.app.AddonManager().Runtime(context.Background(), s.AddonID)
		Expect(err).To(HaveOccurred(), "the frontend runs no add-on")
		u := c.person(s, front)
		u.approve()
		u.connection()

		By("starting on the owner and coming back to the frontend")
		authorize, nonce := u.start(c.owner, s.Optional)
		page := u.arrive(front, u.visit(authorize), nonce)
		Expect(page.Query().Get("oauth")).To(Equal("ok"), page.String())
		Expect(u.tokenCalls(0)).To(HaveLen(1), "the owner traded the code once")
		for _, n := range []*node{c.owner, front} {
			Expect(u.oauth(n)["state"]).To(Equal("connected"))
		}

		By("calling the tool through the frontend, which the owner serves with the token")
		u.expectServed(u.call(front), s.scopes())

		By("renewing a rejected token on the owner for a call through the frontend")
		before := len(u.p.Calls())
		u.expectServed(u.call(front, hash(u.p.LastAccess())), s.scopes())
		Expect(u.tokenCalls(before)).To(HaveLen(1))

		By("refusing a replay of the state on the other node")
		authorize, nonce = u.start(front, nil)
		back := u.visit(authorize)
		Expect(u.arrive(c.owner, back, nonce).Query().Get("oauth")).To(Equal("ok"))
		Expect(u.arrive(front, back, nonce).Query().Get("class")).To(Equal("invalid_state"))

		By("disconnecting through the frontend")
		rec := front.do("alice", http.MethodPost, "/api/chats/connectors/"+u.binding+"/oauth/disconnect", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decode(rec)["revoked"]).To(BeTrue())
		Expect(u.oauth(c.owner)["state"]).To(Equal("not_connected"))
		u.expectClientAuth()
		for _, t := range append(u.texts, c.logText()) {
			u.expectNoSecret(t)
		}
	}, entries("two frontends"))

	It("keeps the logins of the services apart", func() {
		var people []*person
		for _, s := range services {
			u := c.person(s, c.owner)
			u.approve()
			u.connection()
			u.connect(s.Optional)
			people = append(people, u)
		}
		for _, u := range people {
			u.expectServed(u.call(c.owner), u.s.scopes())
			for _, other := range people {
				if other != u {
					Expect(slices.ContainsFunc(other.p.Calls(), func(call oauthtest.Call) bool { return call.Form.Get("client_id") == u.s.ClientID })).
						To(BeFalse(), "%s got a call of %s", other.s.AddonID, u.s.AddonID)
				}
			}
			u.expectClientAuth()
		}
	})
})

func addonUpgrade(id string) addonCommand {
	return addonCommand{OperationID: "upgrade-" + id, AddonID: id, Action: "upgrade", SourceID: "conformance", Version: "1.0.1", Generation: 1}
}
