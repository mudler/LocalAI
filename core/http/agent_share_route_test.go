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

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
		app, s := newAuthModesServer("named-user",
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(authModesHMACSecret))
		models := app.ApplicationConfig().SystemState.Model.ModelsPath
		Expect(os.WriteFile(filepath.Join(models, "share-model.yaml"), []byte("name: share-model\nbackend: no-such-backend\n"), 0o600)).To(Succeed())
		Expect(app.ModelConfigLoader().LoadModelConfigsFromPath(models)).To(Succeed())
		db := app.AuthDB()
		sessions := map[string]string{}
		for _, id := range []string{"alice", "bob"} {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
			Expect(auth.UpdateUserPermissions(db, id, auth.PermissionMap{auth.FeatureAgents: true})).To(Succeed())
			session, err := auth.CreateSession(db, id, authModesHMACSecret)
			Expect(err).NotTo(HaveOccurred())
			sessions[id] = session
		}
		call := func(user, method, path, body, confirm string) (int, string) {
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
		grant := "/api/shares/agent/" + ref.ID + "/grants/bob"
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
})
