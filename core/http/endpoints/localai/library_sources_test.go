package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The routes sit behind auth.RequireAdmin, as RegisterLocalAIRoutes wires them.
var _ = Describe("Library source routes", func() {
	var (
		app      *echo.Echo
		dir      string
		store    *catalog.Sources
		applied  int
		official bool
		dropped  []string
	)

	// call sends one request as the given role ("" = not signed in).
	call := func(role, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if role != "" {
			req.Header.Set("X-Test-Role", role)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	list := func() (sources []map[string]any) {
		rec := call(auth.RoleAdmin, http.MethodGet, "/api/library/sources", "")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var resp struct {
			Sources []map[string]any `json:"sources"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp.Sources
	}
	find := func(id string) map[string]any {
		for _, s := range list() {
			if s["id"] == id {
				return s
			}
		}
		return nil
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		var err error
		store, err = library.Open(dir, true, false)
		Expect(err).NotTo(HaveOccurred())
		applied, official, dropped = 0, true, nil
		app = echo.New()
		app.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if role := c.Request().Header.Get("X-Test-Role"); role != "" {
					c.Set("auth_user", &auth.User{ID: "u-" + role, Role: role})
				}
				return next(c)
			}
		})
		e := LibrarySourcesEndpoints{
			Store:    func() (*catalog.Sources, error) { return store, nil },
			Official: func() bool { return official },
			Applied:  func(d ...string) error { applied++; dropped = append(dropped, d...); return nil },
		}
		admin := auth.RequireAdmin()
		app.GET("/api/library/sources", e.List(), admin)
		app.POST("/api/library/sources", e.Add(), admin)
		app.POST("/api/library/sources/:id/enable", e.SetEnabled(true), admin)
		app.POST("/api/library/sources/:id/disable", e.SetEnabled(false), admin)
		app.PUT("/api/library/sources/:id", e.Update(), admin)
		app.DELETE("/api/library/sources/:id", e.Remove(), admin)
	})

	It("refuses every route to a caller who is not an administrator", func() {
		routes := [][2]string{
			{http.MethodGet, "/api/library/sources"},
			{http.MethodPost, "/api/library/sources"},
			{http.MethodPost, "/api/library/sources/localai-backends/enable"},
			{http.MethodPost, "/api/library/sources/localai-backends/disable"},
			{http.MethodDelete, "/api/library/sources/localai-backends"},
		}
		for _, r := range routes {
			Expect(call(auth.RoleUser, r[0], r[1], `{"kind":"model","url":"https://example.org/m.yaml"}`).Code).To(Equal(http.StatusForbidden), r[0]+" "+r[1])
			Expect(call("", r[0], r[1], `{}`).Code).To(Equal(http.StatusUnauthorized), r[0]+" "+r[1])
		}
		// Nothing was changed by the refused calls.
		Expect(applied).To(BeZero())
		Expect(find("localai-backends")["enabled"]).To(BeTrue())
		Expect(find("localai-backends")["removed"]).To(BeFalse())
	})

	It("lists the sources of all three kinds with their flags", func() {
		kinds := map[any]bool{}
		for _, s := range list() {
			kinds[s["kind"]] = true
			Expect(s["default"]).To(BeTrue())
			Expect(s["removed"]).To(BeFalse())
			Expect(s["suspended"]).To(BeFalse())
		}
		Expect(find("official")["enabled"]).To(BeTrue())
		Expect(find("localai-backends")["enabled"]).To(BeTrue())
		Expect(kinds).To(HaveKey("addon"))
		Expect(kinds).To(HaveKey("backend"))
		Expect(kinds).To(HaveKey("model"))
	})

	It("adds a model source of the user and refuses the same URL twice", func() {
		rec := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", `{"kind":"model","format":"gallery","name":"Team Models","url":"https://example.org/models.yaml"}`)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		var created map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &created)).To(Succeed())
		Expect(created).To(HaveKeyWithValue("id", "team-models"))
		Expect(created).To(HaveKeyWithValue("kind", "model"))
		Expect(created).To(HaveKeyWithValue("default", false))
		Expect(applied).To(Equal(1))

		// The file the CLI reads has it.
		again, err := library.Open(dir, true, false)
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, i := range library.List(again) {
			found = found || i.ID == "team-models"
		}
		Expect(found).To(BeTrue())

		dup := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", `{"kind":"model","format":"gallery","id":"other","url":"https://example.org/models.yaml"}`)
		Expect(dup.Code).To(Equal(http.StatusConflict), dup.Body.String())
		taken := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", `{"kind":"model","id":"team-models","url":"https://example.org/other.yaml"}`)
		Expect(taken.Code).To(Equal(http.StatusConflict), taken.Body.String())
	})

	It("refuses a bad kind, format, id or URL", func() {
		for _, body := range []string{
			`{"kind":"nope","url":"https://example.org/m.yaml"}`,
			`{"kind":"model","format":"zip","url":"https://example.org/m.yaml"}`,
			`{"kind":"model","url":""}`,
			`{"kind":"model","id":"Bad Id","url":"https://example.org/m.yaml"}`,
			`{"kind":"model","url":"ftp://example.org/m.yaml"}`,
			`not json`,
		} {
			Expect(call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", body).Code).To(Equal(http.StatusBadRequest), body)
		}
		Expect(applied).To(BeZero())
	})

	It("disables and enables a default without removing it", func() {
		rec := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources/localai-backends/disable", "")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(find("localai-backends")).To(And(HaveKeyWithValue("enabled", false), HaveKeyWithValue("removed", false)))
		Expect(call(auth.RoleAdmin, http.MethodPost, "/api/library/sources/localai-backends/enable", "").Code).To(Equal(http.StatusOK))
		Expect(find("localai-backends")).To(HaveKeyWithValue("enabled", true))
		Expect(call(auth.RoleAdmin, http.MethodPost, "/api/library/sources/nope/disable", "").Code).To(Equal(http.StatusNotFound))
	})

	It("leaves a tombstone for a removed default, as the CLI does, and deletes a source of the user", func() {
		rec := call(auth.RoleAdmin, http.MethodDelete, "/api/library/sources/localai-backends", "")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"tombstone":true`))
		tomb := find("localai-backends")
		Expect(tomb).To(And(HaveKeyWithValue("removed", true), HaveKeyWithValue("enabled", false), HaveKeyWithValue("default", true)))

		// A restart seeds the defaults again; the tombstone stays.
		again, err := library.Open(dir, true, false)
		Expect(err).NotTo(HaveOccurred())
		for _, i := range library.List(again) {
			if i.ID == "localai-backends" {
				Expect(i.Removed).To(BeTrue())
			}
		}
		// Removing it again finds nothing to remove; enabling brings it back.
		Expect(call(auth.RoleAdmin, http.MethodDelete, "/api/library/sources/localai-backends", "").Code).To(Equal(http.StatusNotFound))
		Expect(call(auth.RoleAdmin, http.MethodPost, "/api/library/sources/localai-backends/enable", "").Code).To(Equal(http.StatusOK))
		Expect(find("localai-backends")).To(And(HaveKeyWithValue("removed", false), HaveKeyWithValue("enabled", true)))

		Expect(call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", `{"kind":"backend","format":"gallery","id":"mine","url":"https://example.org/b.yaml"}`).Code).To(Equal(http.StatusCreated))
		del := call(auth.RoleAdmin, http.MethodDelete, "/api/library/sources/mine", "")
		Expect(del.Code).To(Equal(http.StatusOK))
		Expect(del.Body.String()).To(ContainSubstring(`"tombstone":false`))
		// The server drops the gallery of a deleted source by its address; a
		// tombstone keeps its record, so nothing is dropped for it.
		Expect(dropped).To(Equal([]string{"https://example.org/b.yaml"}))
		Expect(find("mine")).To(BeNil())
	})

	It("shows the defaults as off and suspended while the official libraries are off", func() {
		store.SetOfficial(false)
		official = false
		for _, s := range list() {
			Expect(s).To(HaveKeyWithValue("enabled", false))
			Expect(s).To(HaveKeyWithValue("suspended", true))
			Expect(s).To(HaveKeyWithValue("default", true))
		}
		rec := call(auth.RoleAdmin, http.MethodGet, "/api/library/sources", "")
		Expect(rec.Body.String()).To(ContainSubstring(`"officialLibraries":false`))
	})

	It("carries the conflict message for sources that list one entry", func() {
		serve := func(id string) string {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = catalog.WriteIndex(w, []catalog.Release{{ID: id, Name: id, Version: "1.0.0", Artifact: "oci://registry.example/" + id, Digest: "sha256:" + strings.Repeat("a", 64), OS: "linux", Arch: "amd64"}})
			}))
			DeferCleanup(srv.Close)
			return srv.URL
		}
		for _, id := range []string{"first", "second"} {
			rec := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", `{"kind":"addon","id":"`+id+`","url":"`+serve("tables")+`"}`)
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			_, err := store.Refresh(GinkgoT().Context(), id)
			Expect(err).NotTo(HaveOccurred())
		}
		for _, id := range []string{"first", "second"} {
			msg, _ := find(id)["conflict"].(string)
			Expect(msg).To(ContainSubstring(`"tables"`))
			Expect(msg).To(ContainSubstring("two of your library sources"))
		}
		Expect(find("first")).NotTo(BeNil())
	})

	It("stores mirrors on add, returns them in the list and replaces them with a PUT", func() {
		rec := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", `{"kind":"model","format":"gallery","id":"team","url":"https://example.org/m.yaml","mirrors":[" https://mirror.example/m.yaml ","github:org/repo/m.yaml@main",""]}`)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(find("team")["mirrors"]).To(Equal([]any{"https://mirror.example/m.yaml", "github:org/repo/m.yaml@main"}))
		rec = call(auth.RoleAdmin, http.MethodPut, "/api/library/sources/team", `{"mirrors":["oci://registry.example/m"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(find("team")["mirrors"]).To(Equal([]any{"oci://registry.example/m"}))
		rec = call(auth.RoleAdmin, http.MethodPut, "/api/library/sources/team", `{"mirrors":[]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(find("team")).NotTo(HaveKey("mirrors"))
	})

	It("refuses a mirror that is not an accepted address, and mirrors on an add-on source", func() {
		for _, body := range []string{
			`{"kind":"model","url":"https://example.org/m.yaml","mirrors":["ftp://x/m.yaml"]}`,
			`{"kind":"model","url":"https://example.org/m.yaml","mirrors":["https://a b/m.yaml"]}`,
			`{"kind":"model","url":"https://example.org/m.yaml","mirrors":["1","2","3","4","5","6","7","8","9"]}`,
			`{"kind":"addon","url":"https://example.org/a.yaml","mirrors":["https://mirror.example/a.yaml"]}`,
		} {
			rec := call(auth.RoleAdmin, http.MethodPost, "/api/library/sources", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body+" "+rec.Body.String())
		}
		Expect(call(auth.RoleAdmin, http.MethodPut, "/api/library/sources/localai-backends", `{"mirrors":["file"]}`).Code).To(Equal(http.StatusBadRequest))
		Expect(call(auth.RoleAdmin, http.MethodPut, "/api/library/sources/nope", `{"mirrors":[]}`).Code).To(Equal(http.StatusNotFound))
		Expect(find("localai-backends")).NotTo(HaveKey("mirrors"))
	})
})
