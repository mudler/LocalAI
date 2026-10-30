// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats well-known files endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	put := func(user, name, ifMatch string, body any) *httptest.ResponseRecorder {
		req := s.request(user, http.MethodPut, "/api/chats/wellknown/"+name, body)
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("read", http.MethodGet, "/api/chats/wellknown/persona"),
		Entry("write", http.MethodPut, "/api/chats/wellknown/persona"),
		Entry("revert", http.MethodPost, "/api/chats/wellknown/persona/revert"),
		Entry("schema", http.MethodGet, "/api/chats/wellknown/persona/schema"),
	)

	It("is closed to a user whose chats feature was revoked", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/wellknown/persona", nil).Code).To(Equal(http.StatusForbidden))
	})

	It("reads a missing file as empty and prefills the name from the account", func() {
		rec := s.do("alice", http.MethodGet, "/api/chats/wellknown/user", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Header().Get("ETag")).To(Equal(`"0"`))
		doc := decodeJSON(rec)
		Expect(doc).To(HaveKeyWithValue("exists", false))
		Expect(doc["data"]).To(HaveKeyWithValue("name", "alice"))
		Expect(doc["prefilled"]).To(HaveKeyWithValue("name", "account"))
	})

	It("writes with a version check, keeps unknown keys and answers a conflict with the newer file", func() {
		rec := put("alice", "persona", `"0"`, map[string]any{"values": map[string]any{"name": "Ada", "x_note": "kept"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		v := rec.Header().Get("ETag")
		Expect(v).NotTo(Equal(`"0"`))

		rec = put("alice", "persona", v, map[string]any{"values": map[string]any{"system_prompt": "be brief"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		data := decodeJSON(rec)["data"].(map[string]any)
		Expect(data).To(And(HaveKeyWithValue("name", "Ada"), HaveKeyWithValue("x_note", "kept"), HaveKeyWithValue("system_prompt", "be brief")))

		rec = put("alice", "persona", v, map[string]any{"values": map[string]any{"name": "Stale"}})
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		cur := decodeJSON(rec)["current"].(map[string]any)
		Expect(cur["data"]).To(HaveKeyWithValue("system_prompt", "be brief"))

		Expect(put("alice", "persona", "not-a-number", map[string]any{"values": map[string]any{}}).Code).To(Equal(http.StatusBadRequest))
	})

	It("refuses content that breaks the schema and lists why", func() {
		rec := put("alice", "home", "", map[string]any{"values": map[string]any{"widgets": []any{map[string]any{"id": "Bad Id", "type": "clock", "title": "x"}}}})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		var out struct {
			Errors []string `json:"errors"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Errors).NotTo(BeEmpty())
		Expect(s.do("alice", http.MethodGet, "/api/chats/wellknown/home", nil).Body.String()).To(ContainSubstring(`"exists":false`))
	})

	It("shows a broken file's last good version and reverts to it", func() {
		Expect(put("alice", "shortcuts", "", map[string]any{"values": map[string]any{"shortcuts": []any{map[string]any{"label": "Plan", "prompt": "Plan my day"}}}}).Code).To(Equal(http.StatusOK))
		up := s.do("alice", http.MethodPut, "/api/chats/files/content?path=/shortcuts.json&overwrite=1", `{"shortcuts": "oops"}`)
		Expect(up.Code).To(Equal(http.StatusOK), up.Body.String())

		doc := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/wellknown/shortcuts", nil))
		Expect(doc).To(And(HaveKeyWithValue("valid", false), HaveKeyWithValue("fallback", true)))
		Expect(doc["errors"]).NotTo(BeEmpty())
		Expect(doc["data"].(map[string]any)["shortcuts"]).To(HaveLen(1))

		rec := s.do("alice", http.MethodPost, "/api/chats/wellknown/shortcuts/revert", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("valid", true))
		Expect(s.do("bob", http.MethodPost, "/api/chats/wellknown/shortcuts/revert", nil).Code).To(Equal(http.StatusConflict))
	})

	It("keeps each user's files apart", func() {
		Expect(put("alice", "persona", "", map[string]any{"values": map[string]any{"name": "Ada"}}).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/wellknown/persona", nil))).To(HaveKeyWithValue("exists", false))
	})

	It("reads widget data by path and refuses a path outside the home", func() {
		up := s.do("alice", http.MethodPut, "/api/chats/files/content?path=/data/tasks.json&overwrite=1", `{"items":[{"label":"Call the dentist"}]}`)
		Expect(up.Code).To(Equal(http.StatusOK), up.Body.String())
		rec := s.do("alice", http.MethodGet, "/api/chats/wellknown/widget-data?path=data/tasks.json", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["data"].(map[string]any)["items"]).To(HaveLen(1))
		Expect(s.do("alice", http.MethodGet, "/api/chats/wellknown/widget-data?path=../bob/x.json", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/chats/wellknown/widget-data", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("serves the schema of each file and 404 for an unknown one", func() {
		for _, n := range []string{"persona", "user", "shortcuts", "home", "ui", "widget-data"} {
			rec := s.do("alice", http.MethodGet, "/api/chats/wellknown/"+n+"/schema", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), n)
			Expect(rec.Header().Get("Content-Type")).To(HavePrefix("application/schema+json"))
			Expect(rec.Body.String()).To(ContainSubstring("2020-12"))
		}
		Expect(s.do("alice", http.MethodGet, "/api/chats/wellknown/secrets", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/wellknown/secrets/schema", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses a file over 64 KiB", func() {
		rec := put("alice", "ui", "", map[string]any{"values": map[string]any{"x": strings.Repeat("a", 70<<10)}})
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge), rec.Body.String())
	})

	It("answers 409 with the reason when files are off", func() {
		off := newStudioServerFiles(files.Disabled("There is no data path, so files are off."))
		rec := off.do("alice", http.MethodGet, "/api/chats/wellknown/persona", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring("no data path"))
	})
})
