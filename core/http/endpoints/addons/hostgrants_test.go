// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type hgManager struct {
	submitted []gateway.HostApproval
}

func (m *hgManager) HostAccess(context.Context) ([]gateway.InstalledAccess, error) {
	return []gateway.InstalledAccess{{AddonID: "sandbox", Title: "Sandbox", Version: "1.0.0", State: "running", Identity: "id-sbx", Generation: 1, OperationID: "op-install",
		Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}}, nil
}

func (m *hgManager) PreviewHostAccess(context.Context, string, string, string) (gateway.ReleaseAccess, error) {
	return gateway.ReleaseAccess{AddonID: "newone", Title: "New one", Version: "1.0.0", Digest: "sha256:aa", Required: []string{"workspace.materialize"}}, nil
}

func (m *hgManager) SubmitWithApproval(_ context.Context, _ contract.Actor, cmd managerpkg.LifecycleCommand, ap gateway.HostApproval) (managerpkg.OperationStatus, error) {
	m.submitted = append(m.submitted, ap)
	return managerpkg.OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: "completed"}, nil
}

// registerHostGrants registers the routes as routes/addons.go does, over a
// local owner (no accounts, so the synthetic local owner is the caller).
func registerHostGrants(e *echo.Echo, gw *gateway.Gateway) {
	mode := WithOwnership(auth.AddonModeLocalOwner)
	g := e.Group("/api/addons", BrowserProtection())
	g.GET("/host-grants", GetHostGrants(gw, nil, mode))
	g.POST("/host-grants/preview", PreviewHostGrants(gw, nil, mode))
	g.POST("/host-grants/install", InstallWithHostGrants(gw, nil, mode))
	g.PUT("/host-grants/blanket", SetHostGrantsBlanket(gw, nil, mode))
	g.PUT("/host-grants/deny", SetHostGrantsDeny(gw, nil, mode))
	g.PUT("/host-grants/:addonId", EditHostGrants(gw, nil, mode))
	g.DELETE("/host-grants/:addonId", RevokeHostGrants(gw, nil, mode))
}

var _ = Describe("host access routes", func() {
	const host = "192.0.2.10:8080"
	var (
		e   *echo.Echo
		mgr *hgManager
	)
	BeforeEach(func() {
		store, err := hostgrants.Open(filepath.Join(GinkgoT().TempDir(), "hostgrants.json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		mgr = &hgManager{}
		svc, err := gateway.NewHostGrantsOwner(gateway.HostGrantsConfig{Store: store, Manager: mgr,
			Floor: hostgrants.Floor{Deny: map[string][]string{"*": {"connector."}}},
			Admin: func(context.Context, contract.Actor) error { return nil }})
		Expect(err).NotTo(HaveOccurred())
		e = echo.New()
		registerHostGrants(e, &gateway.Gateway{HostGrantsOwner: svc, Revalidate: func(context.Context, contract.Actor) error { return nil }})
	})

	// confirm is the value the confirm header must carry.
	type req struct{ method, path, body, op string }
	// browser is the administrator's own tab: same-origin fetch, the confirm header.
	browser := func(r *http.Request, op string) {
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		if op != "" {
			r.Header.Set(ConfirmHeader, op)
		}
	}
	do := func(q req, mutate func(*http.Request)) (int, map[string]any) {
		var body io.Reader
		if q.body != "" {
			body = strings.NewReader(q.body)
		}
		r := httptest.NewRequest(q.method, q.path, body)
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		mutate(r)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	errType := func(out map[string]any) string {
		er, _ := out["error"].(map[string]any)
		s, _ := er["type"].(string)
		return s
	}
	asHuman := func(q req) (int, map[string]any) { return do(q, func(r *http.Request) { browser(r, q.op) }) }

	edit := func(op string, rev int, ops string) req {
		return req{"PUT", "/api/addons/host-grants/sandbox", `{"operationId":"` + op + `","revision":` + itoa(rev) + `,"ops":` + ops + `}`, "sandbox"}
	}

	// The write routes, each with a body that decodes, for the guard table.
	writes := []req{
		edit("w1", 0, `["egress.check"]`),
		{"DELETE", "/api/addons/host-grants/sandbox?revision=0&operationId=w2", "", "sandbox"},
		{"PUT", "/api/addons/host-grants/blanket", `{"operationId":"w3","revision":0,"mode":"off"}`, "blanket"},
		{"PUT", "/api/addons/host-grants/deny", `{"operationId":"w4","revision":0,"deny":{}}`, "deny"},
		{"POST", "/api/addons/host-grants/install", `{"operationId":"w5","action":"install","sourceId":"official","addonId":"newone","version":"1.0.0","approve":{"digest":"sha256:aa","ops":[],"revision":0,"nonce":"n"}}`, "newone@sha256:aa"},
	}

	It("serves the reads to an administrator by API key as well", func() {
		for _, q := range []req{{"GET", "/api/addons/host-grants", "", ""}, {"POST", "/api/addons/host-grants/preview", `{"sourceId":"official","addonId":"newone","version":"1.0.0"}`, ""},
			{"PUT", "/api/addons/host-grants/deny", `{"revision":0,"deny":{"sandbox":["workspace."]},"dryRun":true}`, ""}} {
			code, out := do(q, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer an-admin-key")
				r.Header.Set("Sec-Fetch-Site", "same-origin")
			})
			Expect(code).To(Equal(200), q.method+" "+q.path)
			Expect(out).NotTo(BeEmpty())
		}
	})

	It("answers the shapes the React client reads", func() {
		_, ov := do(req{"GET", "/api/addons/host-grants", "", ""}, func(r *http.Request) { browser(r, "") })
		for _, k := range []string{"revision", "locked", "blanket", "floor", "deny", "addons"} {
			Expect(ov).To(HaveKey(k))
		}
		Expect(ov["blanket"]).To(HaveKeyWithValue("setBy", "ui"))
		sbx := ov["addons"].([]any)[0].(map[string]any)
		for _, k := range []string{"id", "title", "version", "sourceId", "state", "requested", "ops", "noLongerRequested", "newOptional", "identityMismatch", "stopped"} {
			if k == "sourceId" {
				continue // empty until the manager reports the library
			}
			Expect(sbx).To(HaveKey(k), k)
		}
		for _, o := range sbx["ops"].([]any) {
			Expect(o.(map[string]any)["status"]).To(BeElementOf("allowed", "not_allowed", "blocked", "unsupported"))
		}
		_, pv := do(req{"POST", "/api/addons/host-grants/preview", `{"sourceId":"official","addonId":"newone","version":"1.0.0"}`, ""}, func(r *http.Request) { browser(r, "") })
		for _, k := range []string{"digest", "version", "revision", "nonce", "official", "added", "requested", "ops", "blockedRequired", "canInstall"} {
			Expect(pv).To(HaveKey(k), k)
		}
	})

	DescribeTable("refuses every write that is not the administrator's own tab, with 403 not_human_surface",
		func(name string, mutate func(*http.Request, string)) {
			for _, q := range writes {
				code, out := do(q, func(r *http.Request) { mutate(r, q.op) })
				Expect(code).To(Equal(403), name+" "+q.method+" "+q.path)
				Expect(errType(out)).To(Equal("not_human_surface"), name+" "+q.path)
			}
		},
		Entry("an API key", "key", func(r *http.Request, op string) { browser(r, op); r.Header.Set("Authorization", "Bearer k") }),
		Entry("an x-api-key header", "xkey", func(r *http.Request, op string) { browser(r, op); r.Header.Set("X-Api-Key", "k") }),
		Entry("the legacy token cookie", "token", func(r *http.Request, op string) { browser(r, op); r.AddCookie(&http.Cookie{Name: "token", Value: "k"}) }),
		Entry("an MCP session", "mcp", func(r *http.Request, op string) { browser(r, op); r.Header.Set("Mcp-Session-Id", "s") }),
		Entry("a tool call", "tool", func(r *http.Request, op string) { browser(r, op); r.Header.Set("X-LocalAI-Tool-Call", "1") }),
		Entry("an agent run", "agent", func(r *http.Request, op string) { browser(r, op); r.Header.Set("X-LocalAI-Agent", "1") }),
		Entry("a cross-site page", "cross", func(r *http.Request, op string) { browser(r, op); r.Header.Set("Sec-Fetch-Site", "same-site") }),
		Entry("a navigation", "nav", func(r *http.Request, op string) { browser(r, op); r.Header.Set("Sec-Fetch-Dest", "document") }),
		Entry("a missing confirm header", "noconfirm", func(r *http.Request, op string) { browser(r, "") }),
		Entry("a confirm header for another operation", "otherconfirm", func(r *http.Request, op string) { browser(r, "someone-else") }),
		Entry("a foreign Origin without fetch metadata", "origin", func(r *http.Request, op string) {
			r.Header.Set("Origin", "http://evil.example")
			r.Header.Set(ConfirmHeader, op)
		}),
	)

	It("accepts a plain-HTTP tab that names this host in Origin when the browser sends no fetch metadata", func() {
		q := edit("o1", 0, `["egress.check"]`)
		code, _ := do(q, func(r *http.Request) { r.Header.Set("Origin", "http://"+host); r.Header.Set(ConfirmHeader, q.op) })
		Expect(code).To(Equal(200))
	})

	It("edits, replays the same operation and refuses a stale revision with 409", func() {
		code, out := asHuman(edit("e1", 0, `["egress.check"]`))
		Expect(code).To(Equal(200))
		Expect(out["revision"]).To(BeEquivalentTo(1))
		code, out = asHuman(edit("e1", 0, `["egress.check"]`))
		Expect(code).To(Equal(200), "the same operation again")
		Expect(out["revision"]).To(BeEquivalentTo(1))
		code, out = asHuman(edit("e2", 0, `["egress.check"]`))
		Expect(code).To(Equal(409))
		Expect(errType(out)).To(Equal("revision_conflict"))
		code, out = asHuman(edit("e1", 1, `[]`))
		Expect(code).To(Equal(409))
		Expect(errType(out)).To(Equal("operation_conflict"))
	})

	It("answers the typed refusals of a bad write", func() {
		code, out := asHuman(edit("e1", 0, `["connector.inbound"]`))
		Expect(code).To(Equal(400), "not declared by the manifest")
		Expect(errType(out)).To(Equal("invalid_request"))
		code, out = asHuman(req{"PUT", "/api/addons/host-grants/ghost", `{"operationId":"e2","revision":0,"ops":["egress.check"]}`, "ghost"})
		Expect(code).To(Equal(404))
		Expect(errType(out)).To(Equal("not_found"))
		code, _ = asHuman(req{"PUT", "/api/addons/host-grants/sandbox", `{"operationId":"e3","revision":0,"ops":[],"surprise":1}`, "sandbox"})
		Expect(code).To(Equal(400), "an unknown field")
		code, _ = asHuman(req{"PUT", "/api/addons/host-grants/sandbox", `{"revision":0,"ops":[]}`, "sandbox"})
		Expect(code).To(Equal(200), "the host makes an operation ID when the caller sent none")
		code, _ = asHuman(req{"DELETE", "/api/addons/host-grants/sandbox?operationId=r1", "", "sandbox"})
		Expect(code).To(Equal(400), "the revision is required")
	})

	It("revokes with the revision in the query", func() {
		asHuman(edit("e1", 0, `["egress.check"]`))
		code, out := asHuman(req{"DELETE", "/api/addons/host-grants/sandbox?revision=0&operationId=r1", "", "sandbox"})
		Expect(code).To(Equal(409), "stale")
		Expect(errType(out)).To(Equal("revision_conflict"))
		code, out = asHuman(req{"DELETE", "/api/addons/host-grants/sandbox?revision=1&operationId=r2", "", "sandbox"})
		Expect(code).To(Equal(200))
		Expect(out["revision"]).To(BeEquivalentTo(2))
	})

	It("previews, then installs with the nonce, and refuses the same approval from the wrong surface", func() {
		code, prev := do(req{"POST", "/api/addons/host-grants/preview", `{"sourceId":"official","addonId":"newone","version":"1.0.0"}`, ""}, func(r *http.Request) { browser(r, "") })
		Expect(code).To(Equal(200))
		nonce := prev["nonce"].(string)
		body := `{"operationId":"i1","action":"install","sourceId":"official","addonId":"newone","version":"1.0.0","approve":{"digest":"sha256:aa","ops":["workspace.materialize"],"revision":0,"nonce":"` + nonce + `"}}`
		code, out := do(req{"POST", "/api/addons/host-grants/install", body, "i1"}, func(r *http.Request) { browser(r, "newone@sha256:aa"); r.Header.Set("Authorization", "Bearer k") })
		Expect(code).To(Equal(403))
		Expect(errType(out)).To(Equal("not_human_surface"))
		Expect(mgr.submitted).To(BeEmpty())
		code, out = asHuman(req{"POST", "/api/addons/host-grants/install", body, "newone@sha256:aa"})
		Expect(code).To(Equal(200))
		Expect(out["Phase"]).To(Equal("completed"))
		Expect(mgr.submitted).To(HaveLen(1))
		code, out = asHuman(req{"POST", "/api/addons/host-grants/install", strings.Replace(body, `"i1"`, `"i2"`, 1), "newone@sha256:aa"})
		Expect(code).To(Equal(409), "the nonce is used up")
		Expect(errType(out)).To(Equal("approval_stale"))
	})

	It("needs the typed text for the blanket, and a human surface for a deny, but not for its dry run", func() {
		q := req{"PUT", "/api/addons/host-grants/blanket", `{"operationId":"b1","revision":0,"mode":"any","confirm":"yes"}`, "blanket"}
		code, out := asHuman(q)
		Expect(code).To(Equal(400))
		Expect(errType(out)).To(Equal("confirmation_mismatch"))
		q.body = `{"operationId":"b2","revision":0,"mode":"any","confirm":"` + gateway.BlanketConfirmAny + `"}`
		q.op = "blanket"
		code, _ = asHuman(q)
		Expect(code).To(Equal(200))
		code, out = asHuman(req{"PUT", "/api/addons/host-grants/deny", `{"operationId":"d1","revision":1,"deny":{"sandbox":["workspace."]}}`, "deny"})
		Expect(code).To(Equal(200))
		Expect(out["revision"]).To(BeEquivalentTo(2))
	})
})

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
