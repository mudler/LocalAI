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
	"github.com/mudler/LocalAI/core/services/studiohistory"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// studioHistoryServer drives the Studio history endpoints through the same
// registration function and middleware chain the real server uses, against an
// in-memory SQLite auth database.
type studioHistoryServer struct {
	e        *echo.Echo
	sessions map[string]string
}

func newStudioHistoryServer() *studioHistoryServer {
	db, err := auth.InitDB(":memory:")
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(sqlDB.Close)

	store, err := studiohistory.New(db)
	Expect(err).NotTo(HaveOccurred())

	s := &studioHistoryServer{e: echo.New(), sessions: map[string]string{}}
	for _, id := range []string{"alice", "bob", "carol"} {
		user := &auth.User{ID: id, Email: id + "@example.test", Name: id, Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(user).Error).To(Succeed())
		session, err := auth.CreateSession(db, id, "")
		Expect(err).NotTo(HaveOccurred())
		s.sessions[id] = session
	}
	// carol had the default-on feature revoked by an admin.
	Expect(auth.UpdateUserPermissions(db, "carol", auth.PermissionMap{auth.FeatureStudioHistory: false})).To(Succeed())

	s.e.Use(auth.Middleware(db, config.NewApplicationConfig()))
	routes.RegisterStudioHistoryRoutes(s.e, store, auth.RequireFeature(db, auth.FeatureStudioHistory))
	return s
}

func (s *studioHistoryServer) do(user, method, path string, body any) *httptest.ResponseRecorder {
	payload := ""
	if raw, ok := body.(string); ok {
		payload = raw
	} else if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		payload = string(data)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if user != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func studioEntryBody(id, mediaType string, createdAt int64) map[string]any {
	return map[string]any{
		"type":      mediaType,
		"createdAt": createdAt,
		"entry": map[string]any{
			"id": id, "prompt": "a grey robot", "model": "ltx", "createdAt": createdAt,
			"params": map[string]any{"size": "768x512"}, "results": []map[string]any{{"url": "/generated-videos/" + id + ".mp4"}},
		},
	}
}

func studioList(s *studioHistoryServer, user, query string) (ids []string, deleted []string, cacheKey string) {
	rec := s.do(user, http.MethodGet, "/api/studio/history"+query, nil)
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	out := decodeJSON(rec)
	ids, deleted = []string{}, []string{}
	for _, e := range out["entries"].([]any) {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	for _, e := range out["deleted"].([]any) {
		deleted = append(deleted, e.(map[string]any)["id"].(string))
	}
	return ids, deleted, out["cacheKey"].(string)
}

var _ = Describe("Studio history endpoints", func() {
	var s *studioHistoryServer

	BeforeEach(func() {
		s = newStudioHistoryServer()
	})

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/studio/history"),
		Entry("clear", http.MethodDelete, "/api/studio/history"),
		Entry("import", http.MethodPost, "/api/studio/history/import"),
		Entry("get", http.MethodGet, "/api/studio/history/e1"),
		Entry("save", http.MethodPut, "/api/studio/history/e1"),
		Entry("delete", http.MethodDelete, "/api/studio/history/e1"),
	)

	DescribeTable("honour a revoked studio_history permission",
		func(method, path string) {
			Expect(s.do("carol", method, path, nil).Code).To(Equal(http.StatusForbidden))
		},
		Entry("list", http.MethodGet, "/api/studio/history"),
		Entry("save", http.MethodPut, "/api/studio/history/e1"),
		Entry("import", http.MethodPost, "/api/studio/history/import"),
	)

	It("answers the list with enabled false and registers nothing else without a store", func() {
		e := echo.New()
		routes.RegisterStudioHistoryRoutes(e, nil, auth.NoopMiddleware())
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/studio/history", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(MatchJSON(`{"enabled": false, "entries": [], "deleted": []}`))

		for _, req := range []*http.Request{
			httptest.NewRequest(http.MethodPut, "/api/studio/history/v1", strings.NewReader(`{}`)),
			httptest.NewRequest(http.MethodDelete, "/api/studio/history", nil),
			httptest.NewRequest(http.MethodPost, "/api/studio/history/import", strings.NewReader(`{}`)),
		} {
			rec = httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Or(Equal(http.StatusNotFound), Equal(http.StatusMethodNotAllowed)), req.Method+" "+req.URL.Path)
		}
	})

	It("says enabled true in the list when it keeps the history", func() {
		rec := s.do("alice", http.MethodGet, "/api/studio/history", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var list map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list["enabled"]).To(BeTrue())
		Expect(list["cacheKey"]).NotTo(BeEmpty())
	})

	It("saves, lists, gets and deletes entries, keeping the entry as sent", func() {
		rec := s.do("alice", http.MethodPut, "/api/studio/history/v1", studioEntryBody("v1", "video", 1000))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["version"]).To(BeEquivalentTo(1))
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/i1", studioEntryBody("i1", "image", 2000)).Code).To(Equal(http.StatusOK))

		listed, _, key := studioList(s, "alice", "")
		Expect(listed).To(Equal([]string{"i1", "v1"}))
		Expect(key).NotTo(BeEmpty())
		videos, _, _ := studioList(s, "alice", "?type=video")
		Expect(videos).To(Equal([]string{"v1"}))

		rec = s.do("alice", http.MethodGet, "/api/studio/history/v1", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		entry := decodeJSON(rec)["entry"].(map[string]any)
		Expect(entry["prompt"]).To(Equal("a grey robot"))
		Expect(entry["results"]).To(HaveLen(1))

		Expect(s.do("alice", http.MethodDelete, "/api/studio/history/v1", nil).Code).To(Equal(http.StatusOK))
		listed, deleted, _ := studioList(s, "alice", "")
		Expect(listed).To(Equal([]string{"i1"}))
		Expect(deleted).To(Equal([]string{"v1"}))
		Expect(s.do("alice", http.MethodGet, "/api/studio/history/v1", nil).Code).To(Equal(http.StatusGone))
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/v1", studioEntryBody("v1", "video", 1000)).Code).To(Equal(http.StatusGone))
	})

	It("keeps users apart and gives each a different cache key", func() {
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/e1", studioEntryBody("e1", "image", 1)).Code).To(Equal(http.StatusOK))
		bobs, _, bobKey := studioList(s, "bob", "")
		Expect(bobs).To(BeEmpty())
		Expect(s.do("bob", http.MethodGet, "/api/studio/history/e1", nil).Code).To(Equal(http.StatusNotFound))
		_, _, aliceKey := studioList(s, "alice", "")
		Expect(aliceKey).NotTo(Equal(bobKey))
	})

	It("imports a browser's entries without deleting anything", func() {
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/kept", studioEntryBody("kept", "image", 1)).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodDelete, "/api/studio/history/dead?type=image", nil).Code).To(Equal(http.StatusOK))
		body := map[string]any{"entries": []map[string]any{
			withID(studioEntryBody("a", "video", 2), "a"),
			withID(studioEntryBody("dead", "image", 3), "dead"),
		}}
		rec := s.do("alice", http.MethodPost, "/api/studio/history/import", body)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		out := decodeJSON(rec)
		Expect(out["count"]).To(BeEquivalentTo(1))
		Expect(out["skipped"]).To(ConsistOf("dead"))
		listed, _, _ := studioList(s, "alice", "")
		Expect(listed).To(ConsistOf("kept", "a"))
	})

	It("clears one workspace", func() {
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/i", studioEntryBody("i", "image", 1)).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/v", studioEntryBody("v", "video", 2)).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodDelete, "/api/studio/history?type=image", nil).Code).To(Equal(http.StatusOK))
		listed, _, _ := studioList(s, "alice", "")
		Expect(listed).To(Equal([]string{"v"}))
	})

	It("rejects malformed ids, types and bodies with 400", func() {
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/bad!id", studioEntryBody("x", "image", 1)).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/x", studioEntryBody("x", "hologram", 1)).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/x", `{"type":"image","entry":"text"}`).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/studio/history/x", `not json`).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/studio/history?type=hologram", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodDelete, "/api/studio/history?type=hologram", nil).Code).To(Equal(http.StatusBadRequest))
	})
})

func withID(body map[string]any, id string) map[string]any {
	body["id"] = id
	return body
}
