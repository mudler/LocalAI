package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeUpdates struct {
	snapshots, checks int
}

func (f *fakeUpdates) UpdatesSnapshot(context.Context) backendupdates.Snapshot {
	f.snapshots++
	return backendupdates.Snapshot{Items: []backendupdates.Item{{ID: "gallery:a", Kind: "gallery", Name: "a"}}, Sources: map[string]backendupdates.Source{"gallery": {State: "ok"}}}
}

func (f *fakeUpdates) CheckNow(context.Context) backendupdates.Snapshot {
	f.checks++
	return backendupdates.Snapshot{CheckedAt: time.Unix(1700000000, 0).UTC(), Items: []backendupdates.Item{}, Sources: map[string]backendupdates.Source{"gallery": {State: "failed", Error: "boom"}}}
}

var _ = Describe("Backend update routes", func() {
	var (
		app *echo.Echo
		p   *fakeUpdates
	)

	call := func(role, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if role != "" {
			req.Header.Set("X-Test-Role", role)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	mount := func(provider BackendUpdatesProvider) {
		app = echo.New()
		app.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if role := c.Request().Header.Get("X-Test-Role"); role != "" {
					c.Set("auth_user", &auth.User{ID: "u-" + role, Role: role})
				}
				return next(c)
			}
		})
		admin := auth.RequireAdmin()
		for _, prefix := range []string{"", "/api"} {
			app.GET(prefix+"/backends/updates", BackendUpdatesEndpoint(provider), admin)
			app.POST(prefix+"/backends/updates/check", CheckBackendUpdatesEndpoint(provider), admin)
		}
	}

	BeforeEach(func() {
		p = &fakeUpdates{}
		mount(p)
	})

	It("serves the read model from the cache to an administrator on both paths", func() {
		for _, path := range []string{"/backends/updates", "/api/backends/updates"} {
			rec := call(auth.RoleAdmin, http.MethodGet, path)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			var got backendupdates.Snapshot
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Items).To(HaveLen(1))
		}
		Expect(p.snapshots).To(Equal(2))
		Expect(p.checks).To(BeZero(), "a read never checks")
	})

	It("checks now on a POST and returns the fresh data, with a failed source as an error", func() {
		for _, path := range []string{"/backends/updates/check", "/api/backends/updates/check"} {
			rec := call(auth.RoleAdmin, http.MethodPost, path)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			var got map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got).To(HaveKey("checkedAt"))
			Expect(got["sources"].(map[string]any)["gallery"].(map[string]any)).To(HaveKeyWithValue("error", "boom"))
		}
		Expect(p.checks).To(Equal(2))
	})

	It("refuses a user who is not an administrator, and a caller who is not signed in", func() {
		for _, r := range [][2]string{
			{http.MethodGet, "/backends/updates"}, {http.MethodGet, "/api/backends/updates"},
			{http.MethodPost, "/backends/updates/check"}, {http.MethodPost, "/api/backends/updates/check"},
		} {
			Expect(call(auth.RoleUser, r[0], r[1]).Code).To(Equal(http.StatusForbidden), r[0]+" "+r[1])
			Expect(call("", r[0], r[1]).Code).To(Equal(http.StatusUnauthorized), r[0]+" "+r[1])
		}
		Expect(p.snapshots + p.checks).To(BeZero())
	})

	It("answers with disabled sources when there is no checker", func() {
		mount(nil)
		for _, r := range [][2]string{{http.MethodGet, "/backends/updates"}, {http.MethodPost, "/api/backends/updates/check"}} {
			rec := call(auth.RoleAdmin, r[0], r[1])
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got backendupdates.Snapshot
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Items).To(BeEmpty())
			Expect(got.Sources["gallery"].State).To(Equal(backendupdates.StateDisabled))
			Expect(got.Sources["library"].State).To(Equal(backendupdates.StateDisabled))
		}
	})
})
