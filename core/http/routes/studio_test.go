// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	studioendpoint "github.com/mudler/LocalAI/core/http/endpoints/studio"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// studioServer drives the studio endpoints through the registration function
// and the middleware chain the real server uses (authentication and the
// route-feature gate), against an in-memory auth database.
type studioServer struct {
	e        *echo.Echo
	harness  *studiotest.GateHarness
	sessions map[string]string
	notify   *notify.Service
}

func newStudioServer() *studioServer { return newStudioServerFiles(newTestFiles()) }

func newStudioServerFiles(f *files.Service) *studioServer { return newStudioServerWith(f, nil) }

// newStudioServerWith lets a spec change the service's dependencies.
func newStudioServerWith(f *files.Service, mod func(*studio.Deps)) *studioServer {
	db, err := auth.InitDB(":memory:")
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(sqlDB.Close)

	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	h := studiotest.NewGateHarness()
	ag, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	ns, err := notify.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	nsvc := notify.NewService(notify.Options{Store: ns})
	deps := studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Files: f, Agents: ag, Notify: nsvc}
	if mod != nil {
		mod(&deps)
	}
	svc, err := studio.New(deps)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })

	s := &studioServer{e: echo.New(), harness: h, sessions: map[string]string{}, notify: nsvc}
	for _, id := range []string{"alice", "bob", "carol", "root"} {
		role := auth.RoleUser
		if id == "root" {
			role = auth.RoleAdmin
		}
		user := &auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(user).Error).To(Succeed())
		session, err := auth.CreateSession(db, id, "")
		Expect(err).NotTo(HaveOccurred())
		s.sessions[id] = session
	}
	// carol had the default-on feature revoked by an admin.
	Expect(auth.UpdateUserPermissions(db, "carol", auth.PermissionMap{auth.FeatureChats: false})).To(Succeed())

	s.e.Use(auth.Middleware(db, config.NewApplicationConfig()))
	s.e.Use(auth.RequireRouteFeature(db))
	models := studioendpoint.ModelCheckerFunc(func(_ context.Context, _ *auth.User, model string) error {
		if model != "test-model" {
			return studioendpoint.ErrUnknownModel
		}
		return nil
	})
	routes.RegisterStudioRoutes(s.e, svc, models)
	return s
}

func (s *studioServer) request(user, method, path string, body any) *http.Request {
	var payload *strings.Reader
	switch b := body.(type) {
	case nil:
		payload = strings.NewReader("")
	case string:
		payload = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		Expect(err).NotTo(HaveOccurred())
		payload = strings.NewReader(string(data))
	}
	req := httptest.NewRequest(method, path, payload)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if user != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
	}
	return req
}

func (s *studioServer) do(user, method, path string, body any) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, s.request(user, method, path, body))
	return rec
}

func (s *studioServer) create(user string) string {
	rec := s.do(user, http.MethodPost, "/api/chats", map[string]any{"model": "test-model"})
	Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
	return decodeJSON(rec)["id"].(string)
}

func (s *studioServer) detail(user, id string) map[string]any {
	rec := s.do(user, http.MethodGet, "/api/chats/"+id, nil)
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	return decodeJSON(rec)
}

func pendingTexts(detail map[string]any) []string {
	out := []string{}
	for _, p := range detail["pending"].([]any) {
		out = append(out, p.(map[string]any)["text"].(string))
	}
	return out
}

var _ = Describe("Chats endpoints", func() {
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("create", http.MethodPost, "/api/chats"),
		Entry("list", http.MethodGet, "/api/chats"),
		Entry("get", http.MethodGet, "/api/chats/c1"),
		Entry("delete", http.MethodDelete, "/api/chats/c1"),
		Entry("messages", http.MethodGet, "/api/chats/c1/messages"),
		Entry("send", http.MethodPost, "/api/chats/c1/messages"),
		Entry("edit pending", http.MethodPatch, "/api/chats/c1/pending/p1"),
		Entry("cancel pending", http.MethodDelete, "/api/chats/c1/pending/p1"),
		Entry("send now", http.MethodPost, "/api/chats/c1/send-now"),
		Entry("stop", http.MethodPost, "/api/chats/c1/stop"),
		Entry("events", http.MethodGet, "/api/chats/c1/events"),
	)

	It("honours a revoked chats permission", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats", nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("carol", http.MethodPost, "/api/chats", map[string]any{"model": "test-model"}).Code).To(Equal(http.StatusForbidden))
	})

	It("rejects a conversation with no model or an unknown one", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats", map[string]any{}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "nope"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats", "not json").Code).To(Equal(http.StatusBadRequest))
	})

	It("lists only the user's own conversations", func() {
		aliceID, bobID := s.create("alice"), s.create("bob")
		rec := s.do("alice", http.MethodGet, "/api/chats", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		ids := []string{}
		for _, c := range decodeJSON(rec)["conversations"].([]any) {
			ids = append(ids, c.(map[string]any)["id"].(string))
		}
		Expect(ids).To(ConsistOf(aliceID))
		Expect(ids).NotTo(ContainElement(bobID))
	})

	It("says whether history survives a restart", func() {
		rec := s.do("alice", http.MethodGet, "/api/chats", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		// This server keeps its chats in memory.
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("persistent", false))
	})

	It("answers not found, never forbidden, for another user's conversation", func() {
		id := s.create("alice")
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/" + id},
			{http.MethodDelete, "/api/chats/" + id},
			{http.MethodGet, "/api/chats/" + id + "/messages"},
			{http.MethodPost, "/api/chats/" + id + "/messages"},
			{http.MethodPatch, "/api/chats/" + id + "/pending/p1"},
			{http.MethodDelete, "/api/chats/" + id + "/pending/p1"},
			{http.MethodPost, "/api/chats/" + id + "/send-now"},
			{http.MethodPost, "/api/chats/" + id + "/stop"},
			{http.MethodGet, "/api/chats/" + id + "/events"},
		} {
			rec := s.do("bob", c.method, c.path, map[string]any{"text": "hi"})
			Expect(rec.Code).To(Equal(http.StatusNotFound), c.method+" "+c.path)
		}
	})

	It("steers a running turn: messages wait, can be edited and cancelled, and arrive as one batch", func() {
		id := s.create("alice")
		base := "/api/chats/" + id

		Expect(s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": ""}).Code).To(Equal(http.StatusBadRequest))
		first := decodeJSON(s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": "start"}))
		Expect(first["started"]).To(BeTrue())
		Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(1))

		a := decodeJSON(s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": "keep"}))
		b := decodeJSON(s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": "typo"}))
		c := decodeJSON(s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": "drop"}))
		Expect(a["started"]).To(BeFalse())
		Expect(a["pending_id"]).NotTo(BeEmpty())
		Expect(pendingTexts(s.detail("alice", id))).To(Equal([]string{"keep", "typo", "drop"}))
		Expect(s.detail("alice", id)["running"]).To(BeTrue())

		Expect(s.do("alice", http.MethodPatch, base+"/pending/"+b["pending_id"].(string), map[string]any{"text": "fixed"}).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodDelete, base+"/pending/"+c["pending_id"].(string), nil).Code).To(Equal(http.StatusOK))
		Expect(pendingTexts(s.detail("alice", id))).To(Equal([]string{"keep", "fixed"}))

		s.harness.Gate <- struct{}{} // the model's next request takes the batch
		Eventually(func() bool { return s.detail("alice", id)["running"].(bool) }, 10*time.Second).Should(BeFalse())
		Expect(s.harness.Delivered()).To(Equal([][]string{{"keep", "fixed"}}))

		// A delivered message can no longer be changed.
		Expect(s.do("alice", http.MethodPatch, base+"/pending/"+a["pending_id"].(string), map[string]any{"text": "late"}).Code).To(Equal(http.StatusConflict))
		Expect(s.do("alice", http.MethodDelete, base+"/pending/"+a["pending_id"].(string), nil).Code).To(Equal(http.StatusConflict))

		rec := s.do("alice", http.MethodGet, base+"/messages", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		texts := []string{}
		for _, m := range decodeJSON(rec)["messages"].([]any) {
			if m.(map[string]any)["role"] == "user" {
				texts = append(texts, m.(map[string]any)["text"].(string))
			}
		}
		Expect(texts).To(Equal([]string{"start", "keep", "fixed"}))
	})

	It("stops a turn and keeps the waiting messages for send-now", func() {
		id := s.create("alice")
		base := "/api/chats/" + id
		s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": "start"})
		Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(1))
		s.do("alice", http.MethodPost, base+"/messages", map[string]any{"text": "z"})

		Expect(s.do("alice", http.MethodPost, base+"/stop", nil).Code).To(Equal(http.StatusOK))
		Eventually(func() bool { return s.detail("alice", id)["running"].(bool) }, 10*time.Second).Should(BeFalse())
		Expect(s.detail("alice", id)["last_turn"]).To(Equal("stopped"))
		Expect(pendingTexts(s.detail("alice", id))).To(Equal([]string{"z"}))

		Expect(s.do("alice", http.MethodPost, base+"/send-now", nil).Code).To(Equal(http.StatusOK))
		Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(2))
	})

	It("deletes a conversation", func() {
		id := s.create("alice")
		Expect(s.do("alice", http.MethodDelete, "/api/chats/"+id, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("streams a conversation's events as server-sent events", func() {
		id := s.create("alice")
		srv := httptest.NewServer(s.e)
		DeferCleanup(srv.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/chats/"+id+"/events", nil)
		Expect(err).NotTo(HaveOccurred())
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions["alice"]})
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))

		go func() {
			defer GinkgoRecover()
			s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "go"})
			Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(1))
			s.harness.Gate <- struct{}{}
		}()

		events := []string{}
		var data []string
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				events = append(events, strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
			if len(events) > 0 && events[len(events)-1] == "turn_end" {
				break
			}
		}
		Expect(events).To(ContainElements("text", "turn_end"))
		var first map[string]any
		Expect(json.Unmarshal([]byte(data[0]), &first)).To(Succeed(), data[0])
		Expect(first).To(HaveKey("kind"))
	})
})
