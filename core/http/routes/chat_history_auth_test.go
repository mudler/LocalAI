// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/chathistory"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// chatHistoryServer drives the conversation endpoints through the same
// registration function and middleware chain the real server uses, against
// an in-memory SQLite auth database.
type chatHistoryServer struct {
	e        *echo.Echo
	db       *gorm.DB
	sessions map[string]string
}

func newChatHistoryServer() *chatHistoryServer {
	db, err := auth.InitDB(":memory:")
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(sqlDB.Close)

	store, err := chathistory.New(db)
	Expect(err).NotTo(HaveOccurred())

	s := &chatHistoryServer{e: echo.New(), db: db, sessions: map[string]string{}}
	for _, id := range []string{"alice", "bob", "carol"} {
		user := &auth.User{ID: id, Email: id + "@example.test", Name: id, Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(user).Error).To(Succeed())
		session, err := auth.CreateSession(db, id, "")
		Expect(err).NotTo(HaveOccurred())
		s.sessions[id] = session
	}
	// carol had the default-on feature revoked by an admin.
	Expect(auth.UpdateUserPermissions(db, "carol", auth.PermissionMap{auth.FeatureChatHistory: false})).To(Succeed())

	s.e.Use(auth.Middleware(db, config.NewApplicationConfig()))
	routes.RegisterChatHistoryRoutes(s.e, store, auth.RequireFeature(db, auth.FeatureChatHistory))
	return s
}

// do sends a request as user (empty = unauthenticated) and returns the
// recorder; body may be nil.
func (s *chatHistoryServer) do(user, method, path string, body any) *httptest.ResponseRecorder {
	var payload *strings.Reader
	if body == nil {
		payload = strings.NewReader("")
	} else if raw, ok := body.(string); ok {
		payload = strings.NewReader(raw)
	} else {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		payload = strings.NewReader(string(data))
	}
	req := httptest.NewRequest(method, path, payload)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if user != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(rec *httptest.ResponseRecorder) map[string]any {
	var out map[string]any
	Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed(), rec.Body.String())
	return out
}

func listedIDs(rec *httptest.ResponseRecorder) []string {
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	ids := []string{}
	for _, c := range decodeJSON(rec)["conversations"].([]any) {
		ids = append(ids, c.(map[string]any)["id"].(string))
	}
	return ids
}

func conversationBody(id, name string) map[string]any {
	return map[string]any{
		"id":      id,
		"name":    name,
		"model":   "test-model",
		"history": []map[string]any{{"role": "user", "content": "hi"}},
	}
}

var _ = Describe("Chat history endpoints", func() {
	var s *chatHistoryServer

	BeforeEach(func() {
		s = newChatHistoryServer()
	})

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/conversations"),
		Entry("create", http.MethodPost, "/api/conversations"),
		Entry("delete all", http.MethodDelete, "/api/conversations"),
		Entry("bulk", http.MethodPut, "/api/conversations/bulk"),
		Entry("get", http.MethodGet, "/api/conversations/c1"),
		Entry("save", http.MethodPut, "/api/conversations/c1"),
		Entry("delete", http.MethodDelete, "/api/conversations/c1"),
	)

	DescribeTable("honour a revoked chat_history permission",
		func(method, path string) {
			Expect(s.do("carol", method, path, nil).Code).To(Equal(http.StatusForbidden))
		},
		Entry("list", http.MethodGet, "/api/conversations"),
		Entry("save", http.MethodPut, "/api/conversations/c1"),
		Entry("bulk", http.MethodPut, "/api/conversations/bulk"),
	)

	It("registers nothing without a store, so the UI falls back to localStorage", func() {
		e := echo.New()
		routes.RegisterChatHistoryRoutes(e, nil, auth.NoopMiddleware())
		req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("saves, lists, gets and deletes a conversation", func() {
		rec := s.do("alice", http.MethodPut, "/api/conversations/c1", conversationBody("c1", "First"))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		saved := decodeJSON(rec)
		Expect(saved["createdAt"]).NotTo(BeZero())
		Expect(saved["updatedAt"]).NotTo(BeZero())

		rec = s.do("alice", http.MethodPost, "/api/conversations", conversationBody("c2", "Second"))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(ConsistOf("c1", "c2"))

		rec = s.do("alice", http.MethodGet, "/api/conversations/c1", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		got := decodeJSON(rec)
		Expect(got["name"]).To(Equal("First"))
		Expect(got["history"]).To(HaveLen(1))

		Expect(s.do("alice", http.MethodDelete, "/api/conversations/c1", nil).Code).To(Equal(http.StatusOK))
		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(ConsistOf("c2"))
	})

	It("takes the id from the path over the body", func() {
		rec := s.do("alice", http.MethodPut, "/api/conversations/from-path", conversationBody("from-body", "x"))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(ConsistOf("from-path"))
	})

	It("returns 404 for an unknown conversation", func() {
		Expect(s.do("alice", http.MethodGet, "/api/conversations/nope", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("rejects malformed ids and bodies with 400", func() {
		Expect(s.do("alice", http.MethodPut, "/api/conversations/bad!id", conversationBody("x", "x")).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/conversations/bad!id", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodDelete, "/api/conversations/bad!id", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/conversations", `{"id":`).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/conversations/bulk", `{"conversations":`).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/conversations/bulk", map[string]any{
			"conversations": []any{conversationBody("ok", "ok"), conversationBody("bad id", "x")},
		}).Code).To(Equal(http.StatusBadRequest))
	})

	It("keeps users apart", func() {
		Expect(s.do("alice", http.MethodPut, "/api/conversations/a1", conversationBody("a1", "alice")).Code).To(Equal(http.StatusOK))
		Expect(listedIDs(s.do("bob", http.MethodGet, "/api/conversations", nil))).To(BeEmpty())
		Expect(s.do("bob", http.MethodGet, "/api/conversations/a1", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("replaces the whole set in bulk and wipes it on delete-all", func() {
		Expect(s.do("alice", http.MethodPut, "/api/conversations/old", conversationBody("old", "old")).Code).To(Equal(http.StatusOK))

		rec := s.do("alice", http.MethodPut, "/api/conversations/bulk", map[string]any{
			"conversations": []any{conversationBody("n1", "n1"), conversationBody("n2", "n2")},
		})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["count"]).To(BeNumerically("==", 2))
		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(ConsistOf("n1", "n2"))

		Expect(s.do("alice", http.MethodDelete, "/api/conversations", nil).Code).To(Equal(http.StatusOK))
		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(BeEmpty())
	})

	// Review on #9902: a conversation deleted on device A must not come back
	// when device B, holding a stale copy, reconnects and writes it again.
	Context("a stale client reconnecting after a remote deletion", func() {
		It("gets 410 for the write, and the deletion stays visible to everyone", func() {
			// Device A and device B both have c1.
			Expect(s.do("alice", http.MethodPut, "/api/conversations/c1", conversationBody("c1", "shared")).Code).To(Equal(http.StatusOK))
			// Device A deletes it.
			Expect(s.do("alice", http.MethodDelete, "/api/conversations/c1", nil).Code).To(Equal(http.StatusOK))

			// Device B comes back with its stale copy and pushes it.
			rec := s.do("alice", http.MethodPut, "/api/conversations/c1", conversationBody("c1", "stale copy"))
			Expect(rec.Code).To(Equal(http.StatusGone), rec.Body.String())
			rec = s.do("alice", http.MethodPost, "/api/conversations", conversationBody("c1", "stale copy"))
			Expect(rec.Code).To(Equal(http.StatusGone), rec.Body.String())

			// The list keeps it out and tells B why, so B can drop its copy
			// instead of treating it as an unsynced draft.
			rec = s.do("alice", http.MethodGet, "/api/conversations", nil)
			Expect(listedIDs(rec)).To(BeEmpty())
			deleted := decodeJSON(rec)["deleted"].([]any)
			Expect(deleted).To(HaveLen(1))
			tomb := deleted[0].(map[string]any)
			Expect(tomb["id"]).To(Equal("c1"))
			Expect(tomb["deletedAt"]).To(BeNumerically(">", 0))
			Expect(tomb["version"]).To(BeNumerically("==", 2))

			Expect(s.do("alice", http.MethodGet, "/api/conversations/c1", nil).Code).To(Equal(http.StatusGone))
		})

		It("is not undone by a bulk upload either", func() {
			Expect(s.do("alice", http.MethodPut, "/api/conversations/c1", conversationBody("c1", "shared")).Code).To(Equal(http.StatusOK))
			Expect(s.do("alice", http.MethodDelete, "/api/conversations", nil).Code).To(Equal(http.StatusOK))

			rec := s.do("alice", http.MethodPut, "/api/conversations/bulk", map[string]any{
				"conversations": []any{conversationBody("c1", "stale"), conversationBody("fresh", "new")},
			})
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			body := decodeJSON(rec)
			Expect(body["count"]).To(BeNumerically("==", 1))
			Expect(body["skipped"]).To(ConsistOf("c1"))
			Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(ConsistOf("fresh"))
		})

		It("holds even when the delete reached the server before the first save", func() {
			Expect(s.do("alice", http.MethodDelete, "/api/conversations/never-saved", nil).Code).To(Equal(http.StatusOK))
			Expect(s.do("alice", http.MethodPut, "/api/conversations/never-saved", conversationBody("never-saved", "late")).Code).To(Equal(http.StatusGone))
		})

		It("leaves a genuinely new conversation alone", func() {
			Expect(s.do("alice", http.MethodPut, "/api/conversations/gone", conversationBody("gone", "x")).Code).To(Equal(http.StatusOK))
			Expect(s.do("alice", http.MethodDelete, "/api/conversations/gone", nil).Code).To(Equal(http.StatusOK))

			rec := s.do("alice", http.MethodPut, "/api/conversations/draft", conversationBody("draft", "new draft"))
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(decodeJSON(rec)["version"]).To(BeNumerically("==", 1))
			// A tombstone is per user: bob may use the same id.
			Expect(s.do("bob", http.MethodPut, "/api/conversations/gone", conversationBody("gone", "bob")).Code).To(Equal(http.StatusOK))
		})
	})
})
