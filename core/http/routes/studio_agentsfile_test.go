// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats agent export, import, templates and history", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("export", http.MethodGet, "/api/chats/agents/a1/export"),
		Entry("import", http.MethodPost, "/api/chats/agents/import"),
		Entry("templates", http.MethodGet, "/api/chats/agents/templates"),
		Entry("use a template", http.MethodPost, "/api/chats/agents/templates/reviewer/create"),
		Entry("runs", http.MethodGet, "/api/chats/agents/a1/runs"),
	)

	save := func(user string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/agents", map[string]any{
			"name": "Reviewer", "description": "reviews", "instructions": "Be brief.", "model": "test-model",
			"addons": []string{"office"}, "skills": []string{"review"}, "collections": []string{"docs"}, "mcp_servers": []string{"wiki"},
		})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}

	It("exports an agent as a file without ID or owner, and imports it as a new agent, also for another user", func() {
		id := save("alice")
		exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil)
		Expect(exp.Code).To(Equal(http.StatusOK))
		Expect(exp.Header().Get("Content-Disposition")).To(ContainSubstring("Reviewer.json"))
		file := decodeJSON(exp)
		Expect(file).To(And(HaveKeyWithValue("version", BeNumerically("==", 1)), HaveKeyWithValue("kind", "localai-agent")))
		def := file["agent"].(map[string]any)
		Expect(def).To(And(HaveKeyWithValue("name", "Reviewer"), HaveKeyWithValue("instructions", "Be brief."), Not(HaveKey("id")), Not(HaveKey("principal"))))
		Expect(def["addons"]).To(ConsistOf("office"))
		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+id+"/export", nil).Code).To(Equal(http.StatusNotFound))

		// Back in for the same user: the name is taken, so the copy is numbered.
		imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		res := decodeJSON(imp)
		Expect(res["agent"]).To(And(HaveKeyWithValue("name", "Reviewer (2)"), HaveKeyWithValue("model", "test-model")))
		Expect(res["agent"].(map[string]any)["id"]).NotTo(Equal(id))
		Expect(res["warnings"]).To(HaveLen(1))

		// For another user the name is free.
		bob := decodeJSON(s.do("bob", http.MethodPost, "/api/chats/agents/import", file))
		Expect(bob["agent"]).To(HaveKeyWithValue("name", "Reviewer"))
		Expect(bob).NotTo(HaveKey("warnings"))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/agents", nil))["agents"]).To(HaveLen(1))
	})

	It("imports a file sent as a multipart form, and drops a model the server does not have", func() {
		file := map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": "Shared", "instructions": "Hi", "model": "model-from-elsewhere"}}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", "shared.json")
		Expect(err).NotTo(HaveOccurred())
		_, err = fw.Write(mustJSON(file))
		Expect(err).NotTo(HaveOccurred())
		Expect(mw.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, "/api/chats/agents/import", &buf)
		req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions["alice"]})
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		res := decodeJSON(rec)
		Expect(res["agent"]).NotTo(HaveKey("model"))
		Expect(res["warnings"]).To(HaveLen(1))
	})

	It("refuses a file that is not an agent, or from a newer format", func() {
		for _, body := range []any{
			"not json at all",
			map[string]any{"version": 1, "kind": "localai-agent"},
			map[string]any{"version": 9, "kind": "localai-agent", "agent": map[string]any{"name": "x"}},
			map[string]any{"version": 1, "kind": "something-else", "agent": map[string]any{"name": "x"}},
			map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": ""}},
		} {
			Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import", body).Code).To(Equal(http.StatusBadRequest), "%v", body)
		}
		// A bare definition, as POST /api/chats/agents takes, is also welcome.
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import", map[string]any{"name": "Bare"}).Code).To(Equal(http.StatusCreated))
	})

	It("offers starter agents and saves one as the user's own", func() {
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/templates", nil))["templates"].([]any)
		ids := []string{}
		for _, t := range list {
			tm := t.(map[string]any)
			ids = append(ids, tm["id"].(string))
			Expect(tm["instructions"]).NotTo(BeEmpty())
		}
		Expect(ids).To(ContainElements("researcher", "reviewer", "digest"))

		rec := s.do("alice", http.MethodPost, "/api/chats/agents/templates/reviewer/create", nil)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		a := decodeJSON(rec)
		Expect(a).To(HaveKeyWithValue("name", "code-reviewer"))
		Expect(a["addons"]).To(BeEmpty())
		Expect(a["mcp_servers"]).To(BeEmpty())
		// The same template twice conflicts on the name; a name of one's own is fine.
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/templates/reviewer/create", nil).Code).To(Equal(http.StatusConflict))
		named := s.do("alice", http.MethodPost, "/api/chats/agents/templates/reviewer/create", map[string]any{"name": "my reviewer"})
		Expect(named.Code).To(Equal(http.StatusCreated))
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/templates/nope/create", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("shows what an agent has run, to its owner only", func() {
		id := save("alice")
		first := decodeJSON(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": id}))["id"].(string)
		second := decodeJSON(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": id}))["id"].(string)
		s.create("alice") // a chat with no agent is not a run
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+first+"/messages", map[string]any{"text": "go"}).Code).To(Equal(http.StatusOK))
		Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(1))
		s.harness.Gate <- struct{}{}
		Eventually(func() any { return s.detail("alice", first)["last_turn"] }, 10*time.Second).Should(Equal("done"))

		hist := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/runs", nil))
		Expect(hist).To(And(HaveKeyWithValue("total", BeNumerically("==", 2)), HaveKeyWithValue("failed", BeNumerically("==", 0)), HaveKeyWithValue("running", BeNumerically("==", 0))))
		runs := hist["runs"].([]any)
		Expect(runs).To(HaveLen(2))
		byID := map[string]map[string]any{}
		for _, r := range runs {
			byID[r.(map[string]any)["chat_id"].(string)] = r.(map[string]any)
		}
		Expect(byID).To(HaveKey(second))
		Expect(byID[first]).To(HaveKeyWithValue("last_turn", "done"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/runs?limit=1", nil))["runs"]).To(HaveLen(1))

		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+id+"/runs", nil).Code).To(Equal(http.StatusNotFound))
	})
})
