// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// shareAPI is the agents API over a real service whose agents are shared
// through a real sharing policy.
type shareAPI struct {
	svc *studiosvc.Service
	pol *policy.Policy
	ag  agents.Store
	hs  *Handlers
	// actors records the principal of each turn, by the text of its message.
	mu     sync.Mutex
	actors map[string]string
}

func newShareAPI(withPolicy bool) *shareAPI {
	names := map[string]string{"alice": "Alice", "bob": "Bob", "carol": "Carol"}
	r := &shareAPI{actors: map[string]string{}}
	h := &studiotest.ToolHarness{Script: func(a workspace.Actor, text string) []studiotest.ToolStep {
		// The text may start with a note of the host; the message ends it.
		r.mu.Lock()
		r.actors[text[strings.LastIndex(text, "\n")+1:]] = a.Principal
		r.mu.Unlock()
		return []studiotest.ToolStep{{Say: "ok"}}
	}}
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.ag = as
	if withPolicy {
		ps, err := policy.OpenStore(GinkgoT().TempDir(), policy.ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ps.Close() })
		r.pol = policy.New(ps, policy.Options{
			Revalidate: func(context.Context, contract.Actor) error { return nil },
			Account: func(_ context.Context, a contract.Actor, _ string) error {
				if _, ok := names[a.UserID]; !ok {
					return policy.ErrForbidden
				}
				return nil
			},
		})
	}
	d := studiosvc.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Agents: as,
		Users: func(_ context.Context, id string) (*auth.User, error) {
			if n, ok := names[id]; ok {
				return &auth.User{ID: id, Name: n, Status: auth.StatusActive}, nil
			}
			return nil, errors.New("no such user")
		}}
	if withPolicy {
		d.AgentShares = studiosvc.PolicyAgentShares{Policy: func() *policy.Policy { return r.pol }}
	}
	r.svc, err = studiosvc.New(d)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = r.svc.Close(context.Background()) })
	if withPolicy {
		Expect(policy.RegisterKinds(r.pol, r.svc)).To(Succeed())
	}
	r.hs = New(r.svc, nil)
	return r
}

func (r *shareAPI) call(user, method, path, body string) (int, string) {
	e := echo.New()
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("auth_user", &auth.User{ID: user, Role: auth.RoleUser})
			// What RequireRouteFeature caches for an account that has the
			// agents feature: the chat route asks for it when a chat names an
			// agent.
			c.Set("auth_permissions", &auth.UserPermission{UserID: user, Permissions: auth.PermissionMap{auth.FeatureAgents: true}})
			return next(c)
		}
	})
	e.GET("/api/chats/agents", r.hs.ListAgents)
	e.GET("/api/chats/agents/:id", r.hs.GetAgent)
	e.PUT("/api/chats/agents/:id", r.hs.UpdateAgent)
	e.DELETE("/api/chats/agents/:id", r.hs.DeleteAgent)
	e.GET("/api/chats/agents/:id/export", r.hs.ExportAgent)
	e.POST("/api/chats/agents/:id/share", r.hs.ShareAgent)
	e.GET("/api/chats/agents/:id/references", r.hs.AgentReferences)
	e.GET("/api/chats/agents/:id/needs", r.hs.AgentNeeds)
	e.GET("/api/chats/agents/:id/runs", r.hs.AgentRuns)
	e.POST("/api/chats", r.hs.Create)
	e.GET("/api/chats/:id", r.hs.Get)
	e.POST("/api/chats/:id/messages", r.hs.Send)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// secretAgent is an agent of alice that names things of hers: a connection,
// an add-on resource and instructions.
func (r *shareAPI) secretAgent() {
	Expect(r.ag.Put(context.Background(), agents.Agent{ID: "ag1", Principal: "alice", Name: "Mailer", Description: "sends mail",
		Instructions: "SECRET-INSTRUCTIONS", Model: "m", Addons: []string{"mail"}, Collections: []string{"docs"},
		Connections: connsets.Sets{"mail": {"binding-of-alice"}},
		Resources:   resourcesets.Sets{"tables": {{ID: "r-alice-table", Mode: "write"}}}, Created: time.Now()})).To(Succeed())
}

func (r *shareAPI) shareWith(principal string, role policy.Role) string {
	code, body := r.call("alice", http.MethodPost, "/api/chats/agents/ag1/share", "")
	Expect(code).To(Equal(http.StatusOK), body)
	var ref schema.ChatsAgentShareRef
	Expect(json.Unmarshal([]byte(body), &ref)).To(Succeed())
	Expect(ref.Kind).To(Equal("agent"))
	owner := contract.Actor{UserID: "alice", CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
	Expect(r.pol.SetGrantRef(context.Background(), owner, policy.Ref{Kind: "agent", ResourceID: ref.ID}, principal, role)).To(Succeed())
	return ref.ID
}

var _ = Describe("the shared agents API", func() {
	It("lists a shared agent with the owner's name and role and nothing of the owner's", func() {
		r := newShareAPI(true)
		r.secretAgent()
		ref := r.shareWith("bob", policy.RoleUse)
		code, body := r.call("bob", http.MethodGet, "/api/chats/agents", "")
		Expect(code).To(Equal(http.StatusOK), body)
		var list schema.ChatsAgentList
		Expect(json.Unmarshal([]byte(body), &list)).To(Succeed())
		Expect(list.Agents).To(HaveLen(1))
		a := list.Agents[0]
		Expect(a.Name).To(Equal("Mailer"))
		Expect(a.Access).NotTo(BeNil())
		Expect(a.Access.Role).To(Equal("use"))
		Expect(a.Access.Owner).To(Equal("Alice"))
		Expect(a.Access.ShareID).To(BeEmpty())
		Expect(a.Access.SharedWith).To(BeNil())
		for _, secret := range []string{"SECRET-INSTRUCTIONS", "binding-of-alice", "r-alice-table", ref, `"alice"`} {
			Expect(body).NotTo(ContainSubstring(secret), "a grantee with use sees %s", secret)
		}
		code, body = r.call("alice", http.MethodGet, "/api/chats/agents", "")
		Expect(code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal([]byte(body), &list)).To(Succeed())
		Expect(list.Agents[0].Access.Role).To(Equal("owner"))
		Expect(*list.Agents[0].Access.SharedWith).To(Equal(1))
		Expect(list.Agents[0].Access.ShareID).To(Equal(ref))
		Expect(list.Agents[0].Access.Sharing).To(BeTrue())
	})

	It("gives a viewer the definition but never the owner's connections, resources or record", func() {
		r := newShareAPI(true)
		r.secretAgent()
		ref := r.shareWith("bob", policy.RoleViewer)
		code, body := r.call("bob", http.MethodGet, "/api/chats/agents/ag1", "")
		Expect(code).To(Equal(http.StatusOK), body)
		Expect(body).To(ContainSubstring("SECRET-INSTRUCTIONS"))
		for _, secret := range []string{"binding-of-alice", "r-alice-table", ref} {
			Expect(body).NotTo(ContainSubstring(secret))
		}
		code, body = r.call("bob", http.MethodGet, "/api/chats/agents/ag1/needs", "")
		Expect(code).To(Equal(http.StatusOK), body)
		var needs schema.ChatsAgentNeedList
		Expect(json.Unmarshal([]byte(body), &needs)).To(Succeed())
		Expect(needs.Needs).NotTo(BeEmpty())
		for _, secret := range []string{"binding-of-alice", "r-alice-table", ref} {
			Expect(body).NotTo(ContainSubstring(secret))
		}
		states := map[string]string{}
		for _, n := range needs.Needs {
			states[n.Kind] = n.State
		}
		Expect(states).To(HaveKeyWithValue("collection", "unavailable"))
		Expect(states).To(HaveKeyWithValue("connection", "missing"))
	})

	It("refuses the owner's actions to a grantee with 403 and hides the agent from others with 404", func() {
		r := newShareAPI(true)
		r.secretAgent()
		r.shareWith("bob", policy.RoleViewer)
		for _, c := range []struct{ method, path, body string }{
			{http.MethodPut, "/api/chats/agents/ag1", `{"name":"Taken"}`},
			{http.MethodDelete, "/api/chats/agents/ag1", ""},
			{http.MethodGet, "/api/chats/agents/ag1/export", ""},
			{http.MethodPost, "/api/chats/agents/ag1/share", ""},
			{http.MethodGet, "/api/chats/agents/ag1/references", ""},
		} {
			code, body := r.call("bob", c.method, c.path, c.body)
			Expect(code).To(Equal(http.StatusForbidden), "%s %s: %s", c.method, c.path, body)
			code, unknown := r.call("carol", c.method, c.path, c.body)
			Expect(code).To(Equal(http.StatusNotFound), "%s %s as a stranger: %s", c.method, c.path, unknown)
			_, missing := r.call("carol", c.method, strings.Replace(c.path, "ag1", "nope", 1), c.body)
			Expect(unknown).To(Equal(missing), "an agent that is not shared reads like one that does not exist")
		}
		for _, p := range []string{"/api/chats/agents/ag1", "/api/chats/agents/ag1/needs", "/api/chats/agents/ag1/runs"} {
			code, _ := r.call("carol", http.MethodGet, p, "")
			Expect(code).To(Equal(http.StatusNotFound), p)
		}
		got, _ := r.ag.Get(context.Background(), "alice", "ag1")
		Expect(got.Name).To(Equal("Mailer"))
	})

	It("lists the references for the owner only, with the shares they can get", func() {
		r := newShareAPI(true)
		r.secretAgent()
		code, body := r.call("alice", http.MethodGet, "/api/chats/agents/ag1/references", "")
		Expect(code).To(Equal(http.StatusOK), body)
		var refs schema.ChatsAgentReferenceList
		Expect(json.Unmarshal([]byte(body), &refs)).To(Succeed())
		kinds := map[string]schema.ChatsAgentReference{}
		for _, x := range refs.References {
			kinds[x.Kind] = x
		}
		Expect(kinds).To(HaveKey("addon"))
		Expect(kinds["collection"].Share).To(BeNil(), "collections are not shareable in this build")
		Expect(kinds["connection"].Share).To(BeNil())
		Expect(kinds["addon-resource"].Share).NotTo(BeNil())
		Expect(kinds["addon-resource"].Share.Kind).To(Equal("addon"))
		Expect(kinds["addon-resource"].Share.Options).To(Equal([]string{"view", "use"}))
		Expect(kinds["addon-resource"].Share.Roles).To(Equal(map[string]string{"view": "viewer", "use": "editor"}), "each option names the role it grants")
	})

	It("lets a grantee start a chat with a shared agent and run it as themselves, until the owner revokes", func() {
		r := newShareAPI(true)
		r.secretAgent()
		ref := r.shareWith("bob", policy.RoleUse)
		code, body := r.call("bob", http.MethodPost, "/api/chats", `{"agent_id":"ag1"}`)
		Expect(code).To(Equal(http.StatusCreated), body)
		var conv schema.ChatsConversation
		Expect(json.Unmarshal([]byte(body), &conv)).To(Succeed())
		Expect(conv.AgentID).To(Equal("ag1"))
		Expect(conv.Model).To(Equal("m"), "the chat takes the model of the shared agent")
		Expect(body).NotTo(ContainSubstring("SECRET-INSTRUCTIONS"))
		code, body = r.call("bob", http.MethodPost, "/api/chats/"+conv.ID+"/messages", `{"text":"first"}`)
		Expect(code).To(Equal(http.StatusOK), body)
		Eventually(func() string {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.actors["first"]
		}).Should(Equal("bob"), "the turn runs as the grantee, with the grantee's own access")
		Eventually(func() bool {
			t, err := r.svc.Turn(context.Background(), "bob", conv.ID)
			return err == nil && !t.Running
		}).Should(BeTrue())
		code, _ = r.call("alice", http.MethodGet, "/api/chats/"+conv.ID, "")
		Expect(code).To(Equal(http.StatusNotFound), "the chat is the grantee's, not the owner's")

		owner := contract.Actor{UserID: "alice", CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
		Expect(r.pol.RevokeRef(context.Background(), owner, policy.Ref{Kind: "agent", ResourceID: ref}, "bob")).To(Succeed())
		code, body = r.call("bob", http.MethodPost, "/api/chats/"+conv.ID+"/messages", `{"text":"again"}`)
		Expect(code).To(Equal(http.StatusConflict), body)
		Expect(body).To(ContainSubstring("agent_not_shared"))
		code, body = r.call("bob", http.MethodPost, "/api/chats", `{"agent_id":"ag1"}`)
		Expect(code).To(Equal(http.StatusNotFound), "a new chat with an agent that is no longer shared: %s", body)
		code, body = r.call("carol", http.MethodPost, "/api/chats", `{"agent_id":"ag1"}`)
		Expect(code).To(Equal(http.StatusNotFound), "a person the agent was never shared with: %s", body)
	})

	It("answers 409 for a share when the server does not share agents", func() {
		r := newShareAPI(false)
		r.secretAgent()
		code, body := r.call("alice", http.MethodPost, "/api/chats/agents/ag1/share", "")
		Expect(code).To(Equal(http.StatusConflict), body)
		Expect(body).To(ContainSubstring("sharing_disabled"))
		code, body = r.call("alice", http.MethodGet, "/api/chats/agents", "")
		Expect(code).To(Equal(http.StatusOK))
		var list schema.ChatsAgentList
		Expect(json.Unmarshal([]byte(body), &list)).To(Succeed())
		Expect(list.Agents[0].Access.Sharing).To(BeFalse())
	})
})
