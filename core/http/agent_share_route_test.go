//go:build auth

package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shareRouteUsers are the account IDs, by name. The collections want an ID
// that is a UUID.
var shareRouteUsers = map[string]string{"alice": "11111111-1111-4111-8111-111111111111", "bob": "22222222-2222-4222-8222-222222222222"}

// newShareRouteServer starts the server with two accounts, alice and bob,
// that hold the agents and collections features and one model, and returns a
// call as one of them from their browser session.
func newShareRouteServer() (*application.Application, func(user, method, path, body, confirm string) (int, string)) {
	app, s := newAuthModesServer("named-user",
		config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(authModesHMACSecret),
		config.WithDataPath(GinkgoT().TempDir()))
	models := app.ApplicationConfig().SystemState.Model.ModelsPath
	Expect(os.WriteFile(filepath.Join(models, "share-model.yaml"), []byte("name: share-model\nbackend: no-such-backend\n"), 0o600)).To(Succeed())
	Expect(app.ModelConfigLoader().LoadModelConfigsFromPath(models)).To(Succeed())
	db := app.AuthDB()
	sessions := map[string]string{}
	for name, id := range shareRouteUsers {
		Expect(db.Create(&auth.User{ID: id, Email: name + "@example.test", Name: name, Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
		Expect(auth.UpdateUserPermissions(db, id, auth.PermissionMap{auth.FeatureAgents: true, auth.FeatureCollections: true})).To(Succeed())
		session, err := auth.CreateSession(db, id, authModesHMACSecret)
		Expect(err).NotTo(HaveOccurred())
		sessions[name] = session
	}
	return app, func(user, method, path, body, confirm string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: sessions[user]})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		if confirm != "" {
			req.Header.Set("X-LocalAI-Confirm", confirm)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
}

// A saved agent shared with another account, through the real server: the
// routes, the authentication and feature middleware, the chats service and
// the sharing policy of the add-on owner.
var _ = Describe("A shared agent over the real routes", func() {
	BeforeEach(func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
	})

	It("lets the grantee start a chat with it and refuses the next message after a revoke", func() {
		_, call := newShareRouteServer()

		code, body := call("alice", http.MethodPost, "/api/chats/agents", `{"name":"Reviewer","model":"share-model","instructions":"Answer in French."}`, "")
		Expect(code).To(Equal(http.StatusCreated), body)
		var agent struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(body), &agent)).To(Succeed())
		code, body = call("alice", http.MethodPost, "/api/chats/agents/"+agent.ID+"/share", "", "")
		Expect(code).To(Equal(http.StatusOK), body)
		var ref struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(body), &ref)).To(Succeed())
		grant := "/api/shares/agent/" + ref.ID + "/grants/" + shareRouteUsers["bob"]
		code, body = call("alice", http.MethodPut, grant, `{"role":"use"}`, ref.ID)
		Expect(code).To(Equal(http.StatusOK), body)

		code, body = call("bob", http.MethodPost, "/api/chats", `{"agent_id":"`+agent.ID+`"}`, "")
		Expect(code).To(Equal(http.StatusCreated), body)
		var chat struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			AgentID string `json:"agent_id"`
		}
		Expect(json.Unmarshal([]byte(body), &chat)).To(Succeed())
		Expect(chat.AgentID).To(Equal(agent.ID))
		Expect(chat.Model).To(Equal("share-model"))
		code, _ = call("bob", http.MethodGet, "/api/chats/"+chat.ID, "", "")
		Expect(code).To(Equal(http.StatusOK), "the chat is the grantee's own")
		code, _ = call("alice", http.MethodGet, "/api/chats/"+chat.ID, "", "")
		Expect(code).To(Equal(http.StatusNotFound), "the owner does not see the grantee's chat")

		code, body = call("alice", http.MethodDelete, grant, "", ref.ID)
		Expect(code).To(Equal(http.StatusOK), body)
		code, body = call("bob", http.MethodPost, "/api/chats/"+chat.ID+"/messages", `{"text":"hello"}`, "")
		Expect(code).To(Equal(http.StatusConflict), body)
		Expect(body).To(ContainSubstring("agent_not_shared"))
		code, body = call("bob", http.MethodPost, "/api/chats", `{"agent_id":"`+agent.ID+`"}`, "")
		Expect(code).To(Equal(http.StatusNotFound), body)
	})

	It("offers each collection the agent names with the roles the collection grants, and the sharing API accepts every offer", func() {
		app, call := newShareRouteServer()
		// The server starts the collections once it listens; no embedding
		// is needed to make one.
		app.StartCollections()
		code, body := call("alice", http.MethodPost, "/api/collections", `{"name":"manuals"}`, "")
		Expect(code).To(Equal(http.StatusCreated), body)
		code, body = call("alice", http.MethodPost, "/api/chats/agents", `{"name":"Reader","model":"share-model","collections":["manuals"]}`, "")
		Expect(code).To(Equal(http.StatusCreated), body)
		var agent struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(body), &agent)).To(Succeed())
		code, body = call("alice", http.MethodGet, "/api/chats/agents/"+agent.ID+"/references", "", "")
		Expect(code).To(Equal(http.StatusOK), body)
		var refs struct {
			References []struct {
				Kind  string `json:"kind"`
				ID    string `json:"id"`
				Share *struct {
					Kind       string            `json:"kind"`
					ResourceID string            `json:"resource_id"`
					Options    []string          `json:"options"`
					Roles      map[string]string `json:"roles"`
				} `json:"share"`
			} `json:"references"`
		}
		Expect(json.Unmarshal([]byte(body), &refs)).To(Succeed())
		Expect(refs.References).To(HaveLen(1))
		share := refs.References[0].Share
		Expect(share).NotTo(BeNil(), "a collection of the owner can be shared from the agent: %s", body)
		Expect(share.Kind).To(Equal("collection"))
		Expect(share.Options).To(Equal([]string{"view"}))
		Expect(share.Roles).To(Equal(map[string]string{"view": "viewer"}))
		// The share route of the collection names the same record.
		code, body = call("alice", http.MethodPost, "/api/collections/manuals/share", "", "")
		Expect(code).To(Equal(http.StatusOK), body)
		Expect(body).To(ContainSubstring(share.ResourceID))
		grant := "/api/shares/collection/" + share.ResourceID + "/grants/" + shareRouteUsers["bob"]
		for _, choice := range share.Options {
			code, body = call("alice", http.MethodPut, grant, `{"role":"`+share.Roles[choice]+`"}`, share.ResourceID)
			Expect(code).To(Equal(http.StatusOK), "%s: %s", choice, body)
		}
		code, body = call("alice", http.MethodPut, grant, `{"role":"use"}`, share.ResourceID)
		Expect(code).To(Equal(http.StatusBadRequest), "use is not a role of a collection: %s", body)
	})
})
