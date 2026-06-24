package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeRecorderBackend lets us assert what the handler asked for without
// pulling in a real GORM/SQLite. The aggregate query is captured so
// the test can verify (a) it ran with the right user/period and (b)
// the JSON shape of the response matches the UI's expectations.
type fakeRecorderBackend struct {
	lastQuery billing.AggregateQuery
	buckets   []auth.UsageBucket
}

func (f *fakeRecorderBackend) Record(_ context.Context, _ *auth.UsageRecord) error { return nil }
func (f *fakeRecorderBackend) Aggregate(_ context.Context, q billing.AggregateQuery) ([]auth.UsageBucket, error) {
	f.lastQuery = q
	return f.buckets, nil
}
func (f *fakeRecorderBackend) Close() error { return nil }

// usageHandler reproduces the /api/usage handler logic from
// routes/usage.go without going through application.Application, which
// drags in galleryop, model loaders, etc. Keeping this tight test
// surface lets the no-auth path (the user-visible feature here) be
// covered without the auth build tag.
func usageHandler(rec *billing.Recorder, fallback *auth.User) echo.HandlerFunc {
	return func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			user = fallback
		}
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		period := c.QueryParam("period")
		if period == "" {
			period = "month"
		}
		buckets, err := rec.Aggregate(c.Request().Context(), billing.AggregateQuery{
			UserID: user.ID,
			Period: period,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "agg failed"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"usage": buckets,
			"viewer": map[string]string{
				"id":   user.ID,
				"name": user.Name,
				"role": user.Role,
			},
		})
	}
}

var _ = Describe("Usage endpoint", func() {
	It("resolves the local user in no-auth mode", func() {
		fb := &fakeRecorderBackend{
			buckets: []auth.UsageBucket{
				{Bucket: "2026-05-05", Model: "qwen-7b", PromptTokens: 100, TotalTokens: 150, RequestCount: 3},
			},
		}
		rec := billing.NewRecorder(fb)
		fallback := &auth.User{ID: "local-uuid", Name: "local", Role: auth.RoleAdmin}

		e := echo.New()
		e.GET("/api/usage", usageHandler(rec, fallback))

		// No Authorization header: simulates --auth=off. The handler must
		// fall through to the fallback user instead of 401-ing.
		req := httptest.NewRequest(http.MethodGet, "/api/usage?period=week", nil)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), "status: got %d, body: %s", w.Code, w.Body.String())
		Expect(fb.lastQuery.UserID).To(Equal("local-uuid"))
		Expect(fb.lastQuery.Period).To(Equal("week"))

		var resp struct {
			Usage []struct {
				Model        string `json:"model"`
				TotalTokens  int64  `json:"total_tokens"`
				RequestCount int64  `json:"request_count"`
			} `json:"usage"`
			Viewer struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"viewer"`
		}
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Usage).To(HaveLen(1))
		Expect(resp.Usage[0].Model).To(Equal("qwen-7b"))
		Expect(resp.Viewer.ID).To(Equal("local-uuid"))
		Expect(resp.Viewer.Name).To(Equal("local"))
	})

	It("returns 401 when there is no user and no fallback", func() {
		rec := billing.NewRecorder(&fakeRecorderBackend{})
		e := echo.New()
		e.GET("/api/usage", usageHandler(rec, nil))

		req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("defaults to month period when none is supplied", func() {
		fb := &fakeRecorderBackend{}
		rec := billing.NewRecorder(fb)
		fallback := &auth.User{ID: "u", Name: "u", Role: auth.RoleAdmin}
		e := echo.New()
		e.GET("/api/usage", usageHandler(rec, fallback))

		req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(fb.lastQuery.Period).To(Equal("month"))
	})
})
